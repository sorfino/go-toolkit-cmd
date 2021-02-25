package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/sorfino/go-toolkit-cmd/cmd/mkpr/internal/options"
	"github.com/sorfino/go-toolkit-cmd/internal/mkpr"
	"golang.org/x/oauth2"
)

var (
	_location *string = flag.String("config", "config.yml", "Location of config file")
	_version  *bool   = flag.Bool("v", false, "Prints current version")
)

func main() {
	flag.Parse()
	if *_version {
		fmt.Println("Go-Toolkit Batch Pull Requester. Version " + options.Version)
		os.Exit(0)
	}

	if err := run(); err != nil {
		fmt.Printf("sorry: %s\n", err.Error())
	}
}

func run() error {
	option, err := options.ParseFile(*_location)
	if err != nil {
		return err
	}

	token := os.Getenv("GITHUB_AUTH_TOKEN")
	if token == "" {
		return errors.New("GITHUB_AUTH_TOKEN not set")
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)

	fmt.Println("hold ...")
	cmd, err := mkpr.NewBatchPullRequestCommand(tc, option)
	if err != nil {
		return err
	}

	done, err := cmd.Do(context.Background())
	for i := range done {
		fmt.Println(done[i])
	}

	if err != nil {
		return err
	}

	fmt.Println("done.")
	return nil
}
