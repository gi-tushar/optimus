package commands

import (
	"fmt"
	"io/ioutil"

	"github.com/odpf/optimus/models"

	"github.com/AlecAivazis/survey/v2"
	cli "github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
	"github.com/odpf/optimus/config"
)

const (
	defaultHost = "localhost"
)

func configCommand(l logger, dsRepo models.DatastoreRepo) *cli.Command {
	c := &cli.Command{
		Use:   "config",
		Short: "Manage opctl configuration required to deploy specifications",
	}
	c.AddCommand(configInitCommand(l, dsRepo))
	return c
}

func configInitCommand(l logger, dsRepo models.DatastoreRepo) *cli.Command {
	c := &cli.Command{
		Use:   "init",
		Short: "Initialize opctl configuration file",
		Run: func(c *cli.Command, args []string) {
			conf := config.Opctl{
				Version: 1,
				Host:    defaultHost,
			}
			questions := []*survey.Question{
				{
					Name: "JobPath",
					Prompt: &survey.Input{
						Message: "Scheduled jobs directory",
						Default: "./jobs",
						Help:    "relative directory path to jobs specification",
					},
					Validate: survey.Required,
				},
				{
					Name: "RegisterConfig",
					Prompt: &survey.Select{
						Message: "Register global configs?",
						Options: []string{"Yes", "No"},
						Default: "No",
					},
				},
			}
			answers := map[string]interface{}{}
			if err := survey.Ask(questions, &answers); err != nil {
				errExit(l, err)
			}
			conf.Job.Path = answers["JobPath"].(string)

			// for global config
			if option, ok := answers["RegisterConfig"]; ok && option.(survey.OptionAnswer).Value == "Yes" {
				conf.Global = map[string]string{}
				registerMore := "Yes"
				for registerMore == "Yes" {
					configAnswers := map[string]interface{}{}
					if err := survey.Ask([]*survey.Question{
						{
							Name: "Name",
							Prompt: &survey.Input{
								Message: "Name of the config",
							},
							Validate: survey.MinLength(3),
						},
						{
							Name: "Value",
							Prompt: &survey.Input{
								Message: "Value",
							},
							Validate: survey.MinLength(1),
						},
					}, &configAnswers); err != nil {
						errExit(l, err)
					}

					if err := survey.AskOne(&survey.Select{
						Message: "Add one more?",
						Options: []string{"Yes", "No"},
						Default: "Yes",
					}, &registerMore); err != nil {
						errExit(l, err)
					}
					conf.Global[configAnswers["Name"].(string)] = configAnswers["Value"].(string)
				}
			}

			// for datastore
			questions = []*survey.Question{
				{
					Name: "RegisterDatastore",
					Prompt: &survey.Select{
						Message: "Register datastore configs?",
						Options: []string{"Yes", "No"},
						Default: "No",
					},
				},
			}
			answers = map[string]interface{}{}
			if err := survey.Ask(questions, &answers); err != nil {
				errExit(l, err)
			}
			if option, ok := answers["RegisterDatastore"]; ok && option.(survey.OptionAnswer).Value == "Yes" {
				dsOptions := []string{}
				for _, ds := range dsRepo.GetAll() {
					dsOptions = append(dsOptions, ds.Name())
				}
				conf.Datastore = []config.Datastore{}
				configAnswers := map[string]interface{}{}
				if err := survey.Ask([]*survey.Question{
					{
						Name: "Type",
						Prompt: &survey.Select{
							Message: "Type of the datastore",
							Options: dsOptions,
						},
					},
					{
						Name: "Path",
						Prompt: &survey.Input{
							Message: "Path for specifications",
						},
						Validate: survey.MinLength(1),
					},
				}, &configAnswers); err != nil {
					errExit(l, err)
				}
				conf.Datastore = append(conf.Datastore, config.Datastore{
					Type: configAnswers["Type"].(survey.OptionAnswer).Value,
					Path: configAnswers["Path"].(string),
				})
			}

			confMarshaled, err := yaml.Marshal(conf)
			if err != nil {
				errExit(l, err)
			}
			if err := ioutil.WriteFile(fmt.Sprintf("%s.%s", ConfigName, ConfigExtension), confMarshaled, 0655); err != nil {
				errExit(l, err)
			}
			l.Println("configuration initialised successfully")
		},
	}
	return c
}