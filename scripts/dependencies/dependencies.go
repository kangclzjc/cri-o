package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/release-sdk/git"
	"sigs.k8s.io/release-utils/command"
)

const (
	branch   = "gh-pages"
	file     = "dependencies.md"
	tokenKey = "GITHUB_TOKEN"
)

var outputPath string

func main() {
	// Parse CLI flags
	flag.StringVar(&outputPath,
		"output-path", "", "the output path for the release notes",
	)
	flag.Parse()

	logrus.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	if err := run(); err != nil {
		logrus.Fatalf("Unable to %v", err)
	}
}

func run() error {
	// Ensure output path
	logrus.Infof("Ensuring output path %s", outputPath)
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		return errors.Wrap(err, "create output path")
	}

	// Generate the report
	logrus.Infof("Getting go modules")
	if err := os.Setenv("GOSUMDB", "off"); err != nil {
		return errors.Wrap(err, "disabling GOSUMDB")
	}
	modules, err := command.New(
		"go", "list", "--mod=mod", "-u", "-m", "--json", "all",
	).RunSilentSuccessOutput()
	if err != nil {
		return errors.Wrap(err, "listing go modules")
	}
	tmpFile, err := os.CreateTemp("", "modules-")
	if err != nil {
		return errors.Wrap(err, "creating temp file")
	}
	if _, err := tmpFile.WriteString(modules.OutputTrimNL()); err != nil {
		return errors.Wrap(err, "writing to temp file")
	}

	logrus.Infof("Retrieving outdated dependencies")
	outdated, err := command.New("cat", tmpFile.Name()).
		Pipe("./build/bin/go-mod-outdated", "--direct", "--update", "--style=markdown").
		RunSuccessOutput()
	if err != nil {
		return errors.Wrap(err, "retrieving outdated dependencies")
	}

	logrus.Infof("Retrieving all dependencies")
	all, err := command.New("cat", tmpFile.Name()).
		Pipe("./build/bin/go-mod-outdated", "--style=markdown").
		RunSuccessOutput()
	if err != nil {
		return errors.Wrap(err, "retrieving all dependencies")
	}

	// Write the output
	outputFile := filepath.Join(outputPath, file)
	os.RemoveAll(outputFile)

	repo, err := git.OpenRepo(".")
	if err != nil {
		return errors.Wrap(err, "open local repo")
	}

	head, err := repo.Head()
	if err != nil {
		return errors.Wrap(err, "get repository HEAD")
	}

	content := fmt.Sprintf(`# CRI-O Dependency Report

_Generated on %s for commit [%s][0]._

[0]: https://github.com/cri-o/cri-o/commit/%s

## Outdated Dependencies

%s

## All Dependencies

%s
`,
		time.Now().Format(time.RFC1123),
		head[:7], head,
		outdated.OutputTrimNL(),
		all.OutputTrimNL(),
	)

	if err := os.WriteFile(outputFile, []byte(content), 0o644); err != nil {
		return errors.Wrap(err, "writing report")
	}

	token, tokenSet := os.LookupEnv(tokenKey)
	if !tokenSet || token == "" {
		logrus.Infof("%s environment variable is not set", tokenKey)
		os.Exit(0)
	}

	currentBranch, err := repo.CurrentBranch()
	if err != nil {
		return errors.Wrap(err, "get current branch")
	}

	logrus.Infof("Checking out branch %s", branch)
	if err := repo.Checkout(branch); err != nil {
		return errors.Wrapf(err, "checkout %s branch", branch)
	}
	defer func() { err = repo.Checkout(currentBranch) }()

	// Write the target file
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return errors.Wrap(err, "write content to file")
	}

	if err := repo.Add(file); err != nil {
		return errors.Wrap(err, "add file to repo")
	}

	// Publish the changes
	if err := repo.Commit("Update dependency report"); err != nil {
		return errors.Wrap(err, "commit")
	}

	if err := repo.Push(branch); err != nil {
		return errors.Wrap(err, "push changes")
	}

	return nil
}
