package bigquery

import (
	"context"
	"net/http"
	"time"

	bqapi "cloud.google.com/go/bigquery"

	"google.golang.org/api/googleapi"

	"github.com/googleapis/google-cloud-go-testing/bigquery/bqiface"
	"github.com/odpf/optimus/models"
	"github.com/pkg/errors"
)

func createExternalTable(ctx context.Context, spec models.ResourceSpec, client bqiface.Client, upsert bool) error {
	bqResource, ok := spec.Spec.(BQTable)

	if !ok {
		return errors.New("failed to read table spec for bigquery")
	}

	// inherit from base
	bqResource.Metadata.Labels = spec.Labels

	dataset := client.DatasetInProject(bqResource.Project, bqResource.Dataset)
	if err := ensureDataset(ctx, dataset, BQDataset{
		Project:  bqResource.Project,
		Dataset:  bqResource.Dataset,
		Metadata: BQDatasetMetadata{},
	}, false); err != nil {
		return err
	}
	table := dataset.Table(bqResource.Table)
	return ensureExternalTable(ctx, table, bqResource, upsert)
}

func ensureExternalTable(ctx context.Context, tableHandle bqiface.Table, t BQTable, upsert bool) error {
	meta, err := tableHandle.Metadata(ctx)
	if err != nil {
		if metaErr, ok := err.(*googleapi.Error); !ok || metaErr.Code != http.StatusNotFound {
			return err
		}
		meta, err := bqCreateTableMetaAdapter(t)
		if err != nil {
			return err
		}

		if t.Metadata.ExpirationTime != "" {
			expiryTime, err := time.Parse(time.RFC3339, t.Metadata.ExpirationTime)
			if err != nil {
				return errors.Wrapf(err, "unable to parse timestamp %s", t.Metadata.ExpirationTime)
			}
			meta.ExpirationTime = expiryTime
		}
		return tableHandle.Create(ctx, meta)
	}
	if !upsert {
		return nil
	}

	// update if already exists
	m := bqapi.TableMetadataToUpdate{
		Description: t.Metadata.Description,
	}
	if t.Metadata.ExpirationTime != "" {
		expiryTime, err := time.Parse(time.RFC3339, t.Metadata.ExpirationTime)
		if err != nil {
			return errors.Wrapf(err, "unable to parse timestamp %s", t.Metadata.ExpirationTime)
		}
		m.ExpirationTime = expiryTime
	}
	for k, v := range t.Metadata.Labels {
		m.SetLabel(k, v)
	}
	if _, err := tableHandle.Update(ctx, m, meta.ETag); err != nil {
		return err
	}
	return nil
}
