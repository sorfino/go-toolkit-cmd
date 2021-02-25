package mkpr

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/github"
)

type Destination struct {
	Repository string `yaml:"repository"`
	Base       string `yaml:"base"`
}

type BatchPullRequestOption struct {
	CommitMessage string        `yaml:"commit_message"` // commit message.
	Subject       string        `yaml:"subject"`        // pull request subject.
	Body          string        `yaml:"body"`           // pull request body.
	Destinations  []Destination `yaml:"destinations"`   // where to create the pull requests.

	// The local file is separated by its target location by a semi-colon.
	// If the file should be in the same location with the same name, you can just put the file name and omit the repetition.
	// Example: README.md,main.go:github/examples/commitpr/main.go
	Files []string `yaml:"files"`

	authorName  string
	authorEmail string
	Head        string `yaml:"head"` // name of the base branch, for instance, "feature/large-scale-change"
}

func (b BatchPullRequestOption) validate() error {
	if b.Head == "" {
		return errors.New("base branch cannot be empty")
	}

	for _, v := range b.Destinations {
		if v.Base == "" {
			return fmt.Errorf("head branc of destination repository %s is empty", v.Repository)
		}

		if b.Head == v.Base {
			return fmt.Errorf("base branch cannot be the same as head at repository %s", v.Repository)
		}
	}

	return nil
}

func (b BatchPullRequestOption) Range(ctx context.Context, f func(option pullRequestCreationOptions) error) error {
	for _, v := range b.Destinations {
		options := pullRequestCreationOptions{
			SourceRepo:         v.Repository,
			BaseBranch:         v.Base,
			CommitMessage:      b.CommitMessage,
			CommitBranch:       b.Head,
			PullRequestRepo:    v.Repository,
			PullRequestBranch:  v.Base,
			PullRequestSubject: b.Subject,
			PullRequestBody:    b.Body,
			Files:              b.Files,
			AuthorName:         b.authorName,
			AuthorEmail:        b.authorEmail,
			SourceOwner:        "mercadolibre",
			PullRequestOwner:   "mercadolibre",
		}
		if err := f(options); err != nil {
			return err
		}
	}

	return nil
}

type pullRequestCreationOptions struct {
	SourceOwner        string   // Name of the owner (user or org) of the repo to create the commit in
	PullRequestOwner   string   // Name of the owner (user or org) of the repo to create the PR against.
	SourceRepo         string   // same as PullRequestRepo
	BaseBranch         string   // develop or master
	CommitMessage      string   // "Automatic Large Scale Change"
	CommitBranch       string   // always options.Base
	PullRequestRepo    string   // destination Repository
	PullRequestBranch  string   // develop or master
	PullRequestSubject string   // your option
	PullRequestBody    string   // your option
	Files              []string // list of files
	AuthorName         string   // f.client.Users.Get(ctx,"") gets the authenticated user.
	AuthorEmail        string
}

type pullRequestCommand struct {
	options pullRequestCreationOptions
	client  *github.Client
}

type BatchPullRequestCommand struct {
	options BatchPullRequestOption
	client  *github.Client
}

func NewBatchPullRequestCommand(tc *http.Client, options BatchPullRequestOption) (*BatchPullRequestCommand, error) {
	// We consider that an error means the branch has not been found and needs to
	// be created.
	if err := options.validate(); err != nil {
		return nil, err
	}

	client := github.NewClient(tc)
	return &BatchPullRequestCommand{
		options: options,
		client:  client,
	}, nil
}

func (f *BatchPullRequestCommand) Do(ctx context.Context) ([]string, error) {
	u, _, err := f.client.Users.Get(context.Background(), "")
	if err != nil {
		return nil, err
	}

	f.options.authorName = u.GetName()
	f.options.authorEmail = u.GetEmail()

	urls := make([]string, 0)
	err = f.options.Range(ctx, func(option pullRequestCreationOptions) error {
		cmd := pullRequestCommand{
			options: option,
			client:  f.client,
		}

		prURL, err := cmd.do(ctx)
		if prURL != "" {
			urls = append(urls, prURL)
		}

		return err
	})

	return urls, err
}

func (f *pullRequestCommand) do(ctx context.Context) (string, error) {
	ref, err := f.getRef(ctx)
	if err != nil {
		return "", err
	}
	if ref == nil {
		return "", errors.New("no error where returned but the reference is nil")
	}

	tree, err := f.getTree(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("unable to create the tree based on the provided files: %w", err)
	}

	if err := f.pushCommit(ctx, ref, tree); err != nil {
		return "", fmt.Errorf("unable to create the commit: %w", err)
	}

	return f.createPR(ctx)
}

// getRef returns the commit branch reference object if it exists or creates it
// from the base branch before returning it.
func (f *pullRequestCommand) getRef(ctx context.Context) (ref *github.Reference, err error) {
	if ref, _, err = f.client.Git.GetRef(ctx, f.options.SourceOwner, f.options.SourceRepo, "refs/heads/"+f.options.CommitBranch); err == nil {
		return ref, nil
	}

	var baseRef *github.Reference
	if baseRef, _, err = f.client.Git.GetRef(ctx, f.options.SourceOwner, f.options.SourceRepo, "refs/heads/"+f.options.BaseBranch); err != nil {
		return nil, fmt.Errorf("unable to get base ref: %w", err)
	}

	newRef := &github.Reference{Ref: github.String("refs/heads/" + f.options.CommitBranch), Object: &github.GitObject{SHA: baseRef.Object.SHA}}
	ref, _, err = f.client.Git.CreateRef(ctx, "mercadolibre", f.options.SourceRepo, newRef)
	return ref, err
}

// getTree generates the tree to commit based on the given files and the commit
// of the ref you got in getRef.
func (f *pullRequestCommand) getTree(ctx context.Context, ref *github.Reference) (tree *github.Tree, err error) {
	// Create a tree with what to commit.
	entries := []github.TreeEntry{}

	// Load each file into the tree.
	for _, fileArg := range f.options.Files {
		file, content, err := getFileContent(fileArg)
		if err != nil {
			return nil, err
		}
		entries = append(entries, github.TreeEntry{Path: github.String(file), Type: github.String("blob"), Content: github.String(string(content)), Mode: github.String("100644")})
	}

	tree, _, err = f.client.Git.CreateTree(ctx, f.options.SourceOwner, f.options.SourceRepo, *ref.Object.SHA, entries)
	return tree, err
}

// getFileContent loads the local content of a file and return the target namex
// of the file in the target repository and its contents.
func getFileContent(fileArg string) (targetName string, b []byte, err error) {
	var localFile string
	files := strings.Split(fileArg, ":")
	switch {
	case len(files) < 1:
		return "", nil, errors.New("empty files")
	case len(files) == 1:
		localFile = files[0]
		targetName = files[0]
	default:
		localFile = files[0]
		targetName = files[1]
	}

	b, err = ioutil.ReadFile(localFile)
	return targetName, b, err
}

// pushCommit creates the commit in the given reference using the given tree.
func (f *pullRequestCommand) pushCommit(ctx context.Context, ref *github.Reference, tree *github.Tree) (err error) {
	// Get the parent commit to attach the commit to.
	parent, _, err := f.client.Repositories.GetCommit(ctx, f.options.SourceOwner, f.options.SourceRepo, *ref.Object.SHA)
	if err != nil {
		return err
	}
	// This is not always populated, but is needed.
	parent.Commit.SHA = parent.SHA

	// Create the commit using the tree.
	date := time.Now()
	author := &github.CommitAuthor{Date: &date, Name: &f.options.AuthorName, Email: &f.options.AuthorEmail}
	commit := &github.Commit{Author: author, Message: &f.options.CommitMessage, Tree: tree, Parents: []github.Commit{*parent.Commit}}
	newCommit, _, err := f.client.Git.CreateCommit(ctx, f.options.SourceOwner, f.options.SourceRepo, commit)
	if err != nil {
		return fmt.Errorf("unable to commit: %w", err)
	}

	// Attach the commit to the master branch.
	ref.Object.SHA = newCommit.SHA
	_, _, err = f.client.Git.UpdateRef(ctx, f.options.SourceOwner, f.options.SourceRepo, ref, false)
	return err
}

// createPR creates a pull request. Based on: https://godoc.org/github.com/google/go-github/github#example-PullRequestsService-Create
func (f *pullRequestCommand) createPR(ctx context.Context) (string, error) {
	newPR := &github.NewPullRequest{
		Title:               &f.options.PullRequestSubject,
		Head:                &f.options.CommitBranch,
		Base:                &f.options.PullRequestBranch,
		Body:                &f.options.PullRequestBody,
		MaintainerCanModify: github.Bool(true),
	}

	pr, _, err := f.client.PullRequests.Create(ctx, f.options.PullRequestOwner, f.options.PullRequestRepo, newPR)
	if err != nil {
		return "", fmt.Errorf("unable to create PR: %w", err)
	}

	return pr.GetHTMLURL(), nil
}
