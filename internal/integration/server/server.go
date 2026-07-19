package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

func strptr(v string) *string { return &v }
func intptr(v int) *int       { return &v }

var repositories = []*Repository{
	{
		ID: "10", Name: "octo-repo", NameWithOwner: "octocat/octo-repo",
		StargazerCount: 42, ViewerHasStarred: false,
	},
	{
		ID: "11", Name: "hello-world", NameWithOwner: "octocat/hello-world",
		StargazerCount: 5, ViewerHasStarred: true,
	},
}

var users = []*User{
	{
		ID: "1", Login: "octocat", Name: strptr("Mona Octocat"), ContributionCount: intptr(17),
		CreatedAt:   strptr("2025-01-01"),
		Status:      &UserStatus{Emoji: strptr(":octocat:")},
		GreatScalar: strptr("cool value"),
	},
	{
		ID: "2", Login: "raven", Name: strptr("Raven"), ContributionCount: intptr(-1),
		Status: nil, GreatScalar: strptr("cool value"),
	},
}

func init() {
	repositories[0].Owner = users[0]
	repositories[1].Owner = users[0]
	users[0].Repositories = []*Repository{repositories[0]}
	users[1].Repositories = []*Repository{}
}

var organizations = []*Organization{
	{
		ID: "3", Login: "octo-org", Name: strptr("Octo Org"),
		Plan: &Plan{Name: PlanNameTeam}, TopContributor: users[0],
		Repositories: []*Repository{repositories[0]},
	},
	{
		ID: "4", Login: "octo-legacy-org", Name: strptr("Octo Legacy Org"),
		Plan: &Plan{Name: PlanNameFree}, TopContributor: nil,
		Repositories: []*Repository{},
	},
}

var issues = []*Issue{
	{ID: "20", Number: 1, Title: "Bug report", Author: users[1], State: IssueStateOpen},
}

var pullRequests = []*PullRequest{
	{ID: "30", Number: 2, Title: "Fix bug", Author: users[0], State: PullRequestStateMerged},
}

var bots = []*Bot{
	{ID: "40", Login: "dependabot"},
}

var comments []*IssueComment

func userByLogin(login string) *User {
	for _, user := range users {
		if login == user.Login {
			return user
		}
	}
	return nil
}

func usersByCreatedAt(dates []string) []*User {
	var retval []*User
	for _, date := range dates {
		for _, user := range users {
			if user.CreatedAt != nil && *user.CreatedAt == date {
				retval = append(retval, user)
			}
		}
	}
	return retval
}

func actorByID(id string) Actor {
	for _, user := range users {
		if id == user.ID {
			return user
		}
	}
	for _, org := range organizations {
		if id == org.ID {
			return org
		}
	}
	return nil
}

func repositoryByID(id string) *Repository {
	for _, repo := range repositories {
		if id == repo.ID {
			return repo
		}
	}
	return nil
}

func getNewCommentID() string {
	maxID := 0
	for _, comment := range comments {
		intID, _ := strconv.Atoi(comment.ID)
		if intID > maxID {
			maxID = intID
		}
	}
	if maxID < 99 {
		maxID = 99
	}
	return strconv.Itoa(maxID + 1)
}

func (r *queryResolver) Viewer(ctx context.Context) (*User, error) {
	return userByLogin("octocat"), nil
}

func (r *queryResolver) User(ctx context.Context, login *string) (*User, error) {
	if login == nil {
		return userByLogin("octocat"), nil
	}
	return userByLogin(*login), nil
}

func (r *queryResolver) Repository(ctx context.Context, owner, name string) (*Repository, error) {
	nameWithOwner := owner + "/" + name
	for _, repository := range repositories {
		if repository.NameWithOwner == nameWithOwner {
			return repository, nil
		}
	}
	return nil, nil
}

func (r *queryResolver) Actor(ctx context.Context, id string) (Actor, error) {
	return actorByID(id), nil
}

func (r *queryResolver) Actors(ctx context.Context, ids []string) ([]Actor, error) {
	ret := make([]Actor, len(ids))
	for i, id := range ids {
		ret[i] = actorByID(id)
	}
	return ret, nil
}

func (r *queryResolver) UsersCreatedOn(ctx context.Context, date string) ([]*User, error) {
	return usersByCreatedAt([]string{date}), nil
}

func (r *queryResolver) UsersCreatedOnDates(ctx context.Context, dates []string) ([]*User, error) {
	return usersByCreatedAt(dates), nil
}

func (r *queryResolver) UserSearch(ctx context.Context, createdOn, login *string) ([]*User, error) {
	switch {
	case createdOn == nil && login != nil:
		return []*User{userByLogin(*login)}, nil
	case createdOn != nil && login == nil:
		return usersByCreatedAt([]string{*createdOn}), nil
	default:
		return nil, fmt.Errorf("need exactly one of createdOn or login")
	}
}

func (r *queryResolver) Search(ctx context.Context, query string, typeArg SearchType) ([]SearchResultItem, error) {
	query = strings.ToLower(query)
	contains := func(s string) bool { return strings.Contains(strings.ToLower(s), query) }

	var results []SearchResultItem
	switch typeArg {
	case SearchTypeRepository:
		for _, repo := range repositories {
			if contains(repo.Name) {
				results = append(results, repo)
			}
		}
	case SearchTypeUser:
		for _, user := range users {
			if contains(user.Login) {
				results = append(results, user)
			}
		}
		for _, org := range organizations {
			if contains(org.Login) {
				results = append(results, org)
			}
		}
		for _, bot := range bots {
			if contains(bot.Login) {
				results = append(results, bot)
			}
		}
	case SearchTypeIssue:
		for _, issue := range issues {
			if contains(issue.Title) {
				results = append(results, issue)
			}
		}
		for _, pr := range pullRequests {
			if contains(pr.Title) {
				results = append(results, pr)
			}
		}
	}
	return results, nil
}

func (r *queryResolver) Fail(ctx context.Context) (*bool, error) {
	f := true
	return &f, fmt.Errorf("oh no")
}

func (m mutationResolver) AddComment(ctx context.Context, input AddCommentInput) (*AddCommentPayload, error) {
	newComment := &IssueComment{ID: getNewCommentID(), Body: input.Body}
	comments = append(comments, newComment)
	return &AddCommentPayload{CommentEdge: &IssueCommentEdge{Node: newComment}}, nil
}

func (m mutationResolver) AddStar(ctx context.Context, input AddStarInput) (*AddStarPayload, error) {
	repo := repositoryByID(input.StarrableID)
	if repo == nil {
		return &AddStarPayload{}, fmt.Errorf("no such starrable %q", input.StarrableID)
	}
	if !repo.ViewerHasStarred {
		repo.ViewerHasStarred = true
		repo.StargazerCount++
	}
	return &AddStarPayload{Starrable: repo}, nil
}

func (m mutationResolver) RemoveStar(ctx context.Context, input RemoveStarInput) (*RemoveStarPayload, error) {
	repo := repositoryByID(input.StarrableID)
	if repo == nil {
		return &RemoveStarPayload{}, fmt.Errorf("no such starrable %q", input.StarrableID)
	}
	if repo.ViewerHasStarred {
		repo.ViewerHasStarred = false
		repo.StargazerCount--
	}
	return &RemoveStarPayload{Starrable: repo}, nil
}

func RunServer() *httptest.Server {
	gqlgenServer := handler.New(NewExecutableSchema(Config{Resolvers: &resolver{}}))
	gqlgenServer.AddTransport(transport.POST{})
	gqlgenServer.AddTransport(transport.GET{})

	return httptest.NewServer(gqlgenServer)
}

type (
	resolver         struct{}
	queryResolver    struct{}
	mutationResolver struct{}
)

func (r *resolver) Mutation() MutationResolver {
	return &mutationResolver{}
}

func (r *resolver) Query() QueryResolver { return &queryResolver{} }

//go:generate go run github.com/99designs/gqlgen@v0.17.94
