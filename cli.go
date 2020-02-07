package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
	yaml "gopkg.in/yaml.v2"
)

func (c *CLI) Run(args []string) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return errors.New("GITHUB_TOKEN is missing")
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token,
	})
	tc := oauth2.NewClient(oauth2.NoContext, ts)
	client := github.NewClient(tc)

	m, err := loadManifest(c.Option.Config)
	if err != nil {
		return err
	}

	gc := &githubClient{
		Client: client,
		dryRun: c.Option.DryRun,
		logger: log.New(os.Stdout, "labeler: ", log.Ldate|log.Ltime),
	}

	if c.Option.DryRun {
		gc.logger.SetPrefix("labeler (dry-run): ")
	}

	c.Client = gc
	c.Config = m

	if len(c.Config.Repos) == 0 {
		return fmt.Errorf("no repos found in %s", c.Option.Config)
	}

	if c.Option.Import {
		m := c.CurrentLabels()
		f, err := os.Create(c.Option.Config)
		if err != nil {
			return err
		}
		defer f.Close()
		return yaml.NewEncoder(f).Encode(&m)
	}

	if cmp.Equal(c.CurrentLabels(), c.Config) {
		// no need to sync
		return nil
	}

	eg := errgroup.Group{}
	for _, repo := range c.Config.Repos {
		repo := repo
		eg.Go(func() error {
			return c.Sync(repo)
		})
	}

	return eg.Wait()
}

// applyLabels creates/edits labels described in YAML
func (c *CLI) applyLabels(owner, repo string, label Label) error {
	ghLabel, err := c.Client.GetLabel(owner, repo, label)
	if err != nil {
		return c.Client.CreateLabel(owner, repo, label)
	}

	if ghLabel.Description != label.Description || ghLabel.Color != label.Color {
		return c.Client.EditLabel(owner, repo, label)
	}

	return nil
}

// deleteLabels deletes the label not described in YAML but exists on GitHub
func (c *CLI) deleteLabels(owner, repo string) error {
	labels, err := c.Client.ListLabels(owner, repo)
	if err != nil {
		return err
	}

	for _, label := range labels {
		if c.Config.checkIfRepoHasLabel(owner+"/"+repo, label.Name) {
			// no need to delete
			continue
		}
		err := c.Client.DeleteLabel(owner, repo, label)
		if err != nil {
			return err
		}
	}

	return nil
}

// Sync syncs labels based on YAML
func (c *CLI) Sync(repo Repo) error {
	slugs := strings.Split(repo.Name, "/")
	if len(slugs) != 2 {
		return fmt.Errorf("repository name %q is invalid", repo.Name)
	}
	for _, labelName := range repo.Labels {
		label, err := c.Config.getDefinedLabel(labelName)
		if err != nil {
			return err
		}
		err = c.applyLabels(slugs[0], slugs[1], label)
		if err != nil {
			return err
		}
	}
	return c.deleteLabels(slugs[0], slugs[1])
}

func (c *CLI) CurrentLabels() Manifest {
	var m Manifest
	for _, repo := range c.Config.Repos {
		e := strings.Split(repo.Name, "/")
		if len(e) != 2 {
			// TODO: handle error
			continue
		}
		labels, err := c.Client.ListLabels(e[0], e[1])
		if err != nil {
			// TODO: handle error
			continue
		}
		var ls []string
		for _, label := range labels {
			ls = append(ls, label.Name)
		}
		repo.Labels = ls
		m.Repos = append(m.Repos, repo)
		m.Labels = append(m.Labels, labels...)
	}
	return m
}
