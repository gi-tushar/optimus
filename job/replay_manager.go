package job

import (
	"context"
	"github.com/odpf/optimus/core/tree"
	"github.com/odpf/optimus/store"
	"sync"
	"time"

	"github.com/odpf/optimus/utils"

	"github.com/google/uuid"
	"github.com/odpf/optimus/core/logger"
	"github.com/odpf/optimus/models"
	"github.com/pkg/errors"
)

var (
	// ErrRequestQueueFull signifies that the deployment manager's
	// request queue is full
	ErrRequestQueueFull = errors.New("request queue is full")
)

type ReplayManagerConfig struct {
	NumWorkers    int
	WorkerTimeout time.Duration
}

type ReplayManager interface {
	Init()
	Replay(*models.ReplayWorkerRequest) (string, error)
}

// Manager for replaying operation(s).
// Offers an asynchronous interface to pipeline, with a fixed size request queue
// Each replay request is handled by a replay worker and the number of parallel replay workers
// can be provided through configuration.
type Manager struct {
	// wait group to synchronise on workers
	wg sync.WaitGroup
	mu sync.Mutex

	uuidProvider utils.UUIDProvider
	config       ReplayManagerConfig

	// request queue, used by workers
	requestQ chan *models.ReplayWorkerRequest
	// request map, used for verifying if a request is
	// in queue without actually consuming it
	requestMap map[uuid.UUID]bool

	//request worker
	replayWorker      ReplayWorker
	replaySpecRepoFac ReplaySpecRepoFactory
}

// Replay a request asynchronously, returns a replay id that can
// can be used to query its status
func (m *Manager) Replay(reqInput *models.ReplayWorkerRequest) (string, error) {
	replaySpecRepo := m.replaySpecRepoFac.New(reqInput.Job)

	err := validate(replaySpecRepo, reqInput)
	if err != nil {
		return "", err
	}

	uuidOb, err := m.uuidProvider.NewUUID()
	if err != nil {
		return "", err
	}
	reqInput.ID = uuidOb

	// save replay request and mark status as accepted
	replay := models.ReplaySpec{
		ID:        uuidOb,
		Job:       reqInput.Job,
		StartDate: reqInput.Start,
		EndDate:   reqInput.End,
		Status:    models.ReplayStatusAccepted,
	}
	if err = replaySpecRepo.Insert(&replay); err != nil {
		return "", err
	}

	// try sending the job request down the request queue
	// if full return error indicating that we don't have capacity
	// to process this request at the moment
	select {
	case m.requestQ <- reqInput:
		m.mu.Lock()
		//request pushed to worker
		m.requestMap[reqInput.ID] = true
		m.mu.Unlock()

		return reqInput.ID.String(), nil
	default:
		return "", ErrRequestQueueFull
	}
}

func validate(replaySpecRepo store.ReplaySpecRepository, reqInput *models.ReplayWorkerRequest) error {
	reqReplayTree, err := prepareTree(reqInput)
	if err != nil {
		return err
	}
	reqReplayNodes := reqReplayTree.GetAllNodes()

	//check if this dag have running instance in Airflow (?)

	//check another replay active for this dag
	statusToValidate := []string{models.ReplayStatusInProgress, models.ReplayStatusAccepted}
	activeReplaySpecs, err := replaySpecRepo.GetByStatus(statusToValidate)
	if err != nil {
		if err == store.ErrResourceNotFound {
			return nil
		}
		return err
	}
	return validateReplayJobsConflict(activeReplaySpecs, reqInput, reqReplayNodes)
}

func validateReplayJobsConflict(activeReplaySpecs []models.ReplaySpec, reqInput *models.ReplayWorkerRequest,
	reqReplayNodes []*tree.TreeNode) error {
	for _, activeSpec := range activeReplaySpecs {
		activeReplayWorkerRequest := &models.ReplayWorkerRequest{
			ID:         activeSpec.ID,
			Job:        activeSpec.Job,
			Start:      activeSpec.StartDate,
			End:        activeSpec.EndDate,
			Project:    reqInput.Project,
			JobSpecMap: reqInput.JobSpecMap,
		}
		activeTree, err := prepareTree(activeReplayWorkerRequest)
		if err != nil {
			return err
		}
		activeNodes := activeTree.GetAllNodes()

		return checkAnyConflictedDags(activeNodes, reqReplayNodes)
	}
	return nil
}

func checkAnyConflictedDags(activeNodes []*tree.TreeNode, reqReplayNodes []*tree.TreeNode) error {
	for _, activeNode := range activeNodes {
		for _, reqNode := range reqReplayNodes {
			if activeNode.Data.GetName() == reqNode.Data.GetName() {
				return checkAnyConflictedRuns(activeNode, reqNode)
			}
		}
	}
	return nil
}

func checkAnyConflictedRuns(activeNode *tree.TreeNode, reqNode *tree.TreeNode) error {
	for _, activeNodeRun := range activeNode.Runs.Values() {
		for _, reqNodeRun := range reqNode.Runs.Values() {
			if activeNodeRun == reqNodeRun {
				return errors.New("conflict msg")
			}
		}
	}
	return nil
}

// start a worker goroutine that runs the deployment pipeline in background
func (m *Manager) spawnServiceWorker() {
	defer m.wg.Done()

	for reqInput := range m.requestQ {
		logger.I("worker picked up the request for ", reqInput.Job.Name)
		ctx, cancelCtx := context.WithTimeout(context.Background(), m.config.WorkerTimeout)
		if err := m.replayWorker.Process(ctx, reqInput); err != nil {
			//do something about this error
			logger.E(errors.Wrap(err, "worker failed to process"))
			cancelCtx()
		}
		cancelCtx()
	}
}

//Close stops consuming any new request
func (m *Manager) Close() error {
	if m.requestQ != nil {
		//stop accepting any more requests
		close(m.requestQ)
	}

	//wait for request worker to finish
	m.wg.Wait()

	return nil
}

func (m *Manager) Init() {
	logger.I("starting replay workers")
	for i := 0; i < m.config.NumWorkers; i++ {
		m.wg.Add(1)
		go m.spawnServiceWorker()
	}
}

// NewManager constructs a new instance of Manager
func NewManager(worker ReplayWorker, replaySpecRepoFac ReplaySpecRepoFactory, uuidProvider utils.UUIDProvider, config ReplayManagerConfig) *Manager {
	mgr := &Manager{
		replayWorker:      worker,
		requestMap:        make(map[uuid.UUID]bool),
		config:            config,
		requestQ:          make(chan *models.ReplayWorkerRequest, 0),
		replaySpecRepoFac: replaySpecRepoFac,
		uuidProvider:      uuidProvider,
	}
	mgr.Init()
	return mgr
}