package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	var err error
	defer func() {
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}()

	key := os.Getenv("GITHUB_TOKEN")
	if key == "" {
		err = fmt.Errorf("must set GITHUB_TOKEN=<github token>")
		return
	}

	graphqlClient := NewClient("https://api.github.com/graphql", nil)
	err = graphqlClient.SetBearerToken(key)
	if err != nil {
		return
	}

	switch len(os.Args) {
	case 1:
		var viewerResp *getViewerResponse
		viewerResp, err = graphqlClient.getViewer(context.Background())
		if err != nil {
			return
		}
		fmt.Println(
			"you are",
			viewerResp.Viewer.MyName,
			"created on",
			viewerResp.Viewer.CreatedAt.Format("2006-01-02"),
		)

	case 2:
		username := os.Args[1]
		var userResp *getUserResponse
		userResp, err = graphqlClient.getUser(context.Background(), getUserVariables{
			Login: username,
		})
		if err != nil {
			return
		}
		fmt.Println(
			username,
			"is",
			userResp.User.TheirName,
			"created on",
			userResp.User.CreatedAt.Format("2006-01-02"),
		)

	default:
		err = fmt.Errorf("usage: %v [username]", os.Args[0])
	}
}

//go:generate go run github.com/willabides/octoql/cmd/octoqlgen generate --config octoqlgen.yaml
