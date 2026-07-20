// Package integration contains octoqlgen's integration tests, which run
// against a real server (defined in internal/integration/server/server.go).
//
// These are especially important for cases where we generate nontrivial logic,
// such as JSON-unmarshaling.
package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willabides/octoql"
	"github.com/willabides/octoql/internal/generate"
	gqlserver "github.com/willabides/octoql/internal/integration/server"
)

func TestGetRepository(t *testing.T) {
	_ = `# @octoqlgen
	query getRepository($owner: String!, $name: String!) {
		repository(owner: $owner, name: $name) {
			id
			name
			nameWithOwner
			owner { login }
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := getRepository(ctx, client, getRepositoryVariables{
			Owner: "octocat",
			Name:  "octo-repo",
		})
		require.NoError(t, err)

		require.NotNil(t, response.Repository)
		assert.Equal(t, "10", response.Repository.Id)
		assert.Equal(t, "octo-repo", response.Repository.Name)
		assert.Equal(t, "octocat/octo-repo", response.Repository.NameWithOwner)
		assert.Equal(t, "octocat", response.Repository.Owner.GetLogin())
	}
}

func TestMutation(t *testing.T) {
	_ = `# @octoqlgen
	mutation addComment($input: AddCommentInput!) {
		addComment(input: $input) { commentEdge { node { id body } } }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	postClient := newRoundtripClient(server.URL)

	response, err := addComment(ctx, postClient, addCommentVariables{
		Input: gqlserver.AddCommentInput{
			SubjectID: "20",
			Body:      "Thanks for reporting!",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, response.AddComment.CommentEdge)
	require.NotNil(t, response.AddComment.CommentEdge.Node)
	assert.Equal(t, "Thanks for reporting!", response.AddComment.CommentEdge.Node.Body)
}

func TestStarMutations(t *testing.T) {
	_ = `# @octoqlgen
	mutation addStar($input: AddStarInput!) {
		addStar(input: $input) { starrable { id stargazerCount viewerHasStarred } }
	}
	mutation removeStar($input: RemoveStarInput!) {
		removeStar(input: $input) { starrable { id stargazerCount viewerHasStarred } }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	postClient := newRoundtripClient(server.URL)

	starResponse, err := addStar(ctx, postClient, addStarVariables{
		Input: gqlserver.AddStarInput{StarrableID: "10"},
	})
	require.NoError(t, err)
	require.NotNil(t, starResponse.AddStar.Starrable)
	assert.Equal(t, "10", (*starResponse.AddStar.Starrable).GetId())
	assert.True(t, (*starResponse.AddStar.Starrable).GetViewerHasStarred())
	assert.Equal(t, 43, (*starResponse.AddStar.Starrable).GetStargazerCount())

	unstarResponse, err := removeStar(ctx, postClient, removeStarVariables{
		Input: gqlserver.RemoveStarInput{StarrableID: "10"},
	})
	require.NoError(t, err)
	require.NotNil(t, unstarResponse.RemoveStar.Starrable)
	assert.Equal(t, "10", (*unstarResponse.RemoveStar.Starrable).GetId())
	assert.False(t, (*unstarResponse.RemoveStar.Starrable).GetViewerHasStarred())
	assert.Equal(t, 42, (*unstarResponse.RemoveStar.Starrable).GetStargazerCount())
}

func TestServerError(t *testing.T) {
	_ = `# @octoqlgen
	query failingQuery { fail viewer { id } }`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := failingQuery(ctx, client)
		assert.Error(t, err)
		t.Logf("Full error: %+v", err)
		var gqlErrors octoql.Errors
		if !assert.True(t, errors.As(err, &gqlErrors), "error should contain octoql.Errors") {
			t.Logf("Actual error type: %T", err)
			t.Logf("Error message: %v", err)
		} else {
			assert.Len(t, gqlErrors, 1, "Expected one GraphQL error")
			assert.Equal(t, "oh no", gqlErrors[0].Message)
		}
		assert.Nil(t, response)
		partialErr, ok := errors.AsType[*failingQueryPartialDataError](err)
		require.True(t, ok)
		require.NotNil(t, partialErr.PartialData())
		assert.Equal(t, "1", partialErr.PartialData().Viewer.Id)
	}
}

func TestVariables(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithVariables($login: String!) { user(login: $login) { id login contributionCount } }`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := []*octoql.Client{octoql.NewClient(server.URL, http.DefaultClient)}

	for _, client := range clients {
		response, err := queryWithVariables(ctx, client, queryWithVariablesVariables{
			Login: "raven",
		})
		require.NoError(t, err)
		require.NotNil(t, response.User)

		assert.Equal(t, "2", response.User.Id)
		assert.Equal(t, "raven", response.User.Login)
		assert.Equal(t, -1, requirePtrValue(t, response.User.ContributionCount))

		response, err = queryWithVariables(ctx, client, queryWithVariablesVariables{
			Login: "definitely-not-a-real-login",
		})
		require.NoError(t, err)

		assert.Nil(t, response.User)
	}
}

func TestOmitempty(t *testing.T) {
	_ = `# @octoqlgen(omitempty: true)
	query queryWithOmitempty($login: String) {
		user(login: $login) { id login contributionCount }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithOmitempty(ctx, client, queryWithOmitemptyVariables{
			Login: ptr("raven"),
		})
		require.NoError(t, err)
		require.NotNil(t, response.User)

		assert.Equal(t, "2", response.User.Id)
		assert.Equal(t, "raven", response.User.Login)
		assert.Equal(t, -1, requirePtrValue(t, response.User.ContributionCount))

		// should return the default viewer-like user, not the user with login ""
		response, err = queryWithOmitempty(ctx, client, queryWithOmitemptyVariables{})
		require.NoError(t, err)
		require.NotNil(t, response.User)

		assert.Equal(t, "1", response.User.Id)
		assert.Equal(t, "octocat", response.User.Login)
		assert.Equal(t, 17, requirePtrValue(t, response.User.ContributionCount))
	}
}

func TestCustomMarshal(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithCustomMarshal($date: Date!) {
		usersCreatedOn(date: $date) { id login createdAt }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshal(ctx, client, queryWithCustomMarshalVariables{
			Date: time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC),
		})
		require.NoError(t, err)

		assert.Len(t, response.UsersCreatedOn, 1)
		user := response.UsersCreatedOn[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			requirePtrValue(t, user.CreatedAt))

		response, err = queryWithCustomMarshal(ctx, client, queryWithCustomMarshalVariables{
			Date: time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC),
		})
		require.NoError(t, err)
		assert.Len(t, response.UsersCreatedOn, 0)
	}
}

func TestCustomMarshalSlice(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithCustomMarshalSlice($dates: [Date!]!) {
		usersCreatedOnDates(dates: $dates) { id login createdAt }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshalSlice(ctx, client, queryWithCustomMarshalSliceVariables{
			Dates: []time.Time{time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC)},
		})
		require.NoError(t, err)

		assert.Len(t, response.UsersCreatedOnDates, 1)
		user := response.UsersCreatedOnDates[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			requirePtrValue(t, user.CreatedAt))

		response, err = queryWithCustomMarshalSlice(ctx, client, queryWithCustomMarshalSliceVariables{
			Dates: []time.Time{time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC)},
		})
		require.NoError(t, err)
		assert.Len(t, response.UsersCreatedOnDates, 0)
	}
}

func TestCustomMarshalOptional(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithCustomMarshalOptional(
		# @octoqlgen(pointer: true)
		$date: Date,
		# @octoqlgen(pointer: true)
		$login: String,
	) {
		userSearch(createdOn: $date, login: $login) { id login createdAt }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		date := time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC)
		response, err := queryWithCustomMarshalOptional(ctx, client, queryWithCustomMarshalOptionalVariables{
			Date: &date,
		})
		require.NoError(t, err)

		assert.Len(t, response.UserSearch, 1)
		user := response.UserSearch[0]
		require.NotNil(t, user)
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			requirePtrValue(t, user.CreatedAt))

		login := "raven"
		response, err = queryWithCustomMarshalOptional(ctx, client, queryWithCustomMarshalOptionalVariables{
			Login: &login,
		})
		require.NoError(t, err)
		assert.Len(t, response.UserSearch, 1)
		user = response.UserSearch[0]
		require.NotNil(t, user)
		assert.Equal(t, "2", user.Id)
		assert.Equal(t, "raven", user.Login)
		assert.Nil(t, user.CreatedAt)
	}
}

func TestInterfaceNoFragments(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithInterfaceNoFragments($id: ID!) {
		actor(id: $id) { id login }
		viewer { id login }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceNoFragments(ctx, client, queryWithInterfaceNoFragmentsVariables{
			Id: "1",
		})
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: User{Id: 1, Login: "octocat"},

		assert.Equal(t, "1", response.Viewer.Id)
		assert.Equal(t, "octocat", response.Viewer.Login)

		// Check fields both via interface and via type-assertion:
		require.NotNil(t, response.Actor)
		actor := *response.Actor
		assert.Equal(t, "User", requirePtrValue(t, actor.GetTypename()))
		assert.Equal(t, "1", actor.GetId())
		assert.Equal(t, "octocat", actor.GetLogin())

		user, ok := actor.(*queryWithInterfaceNoFragmentsActorUser)
		require.Truef(t, ok, "got %T, not User", actor)
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		response, err = queryWithInterfaceNoFragments(ctx, client, queryWithInterfaceNoFragmentsVariables{
			Id: "3",
		})
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: Organization{Id: 3, Login: "octo-org"},

		assert.Equal(t, "1", response.Viewer.Id)
		assert.Equal(t, "octocat", response.Viewer.Login)

		require.NotNil(t, response.Actor)
		actor = *response.Actor
		assert.Equal(t, "Organization", requirePtrValue(t, actor.GetTypename()))
		assert.Equal(t, "3", actor.GetId())
		assert.Equal(t, "octo-org", actor.GetLogin())

		org, ok := actor.(*queryWithInterfaceNoFragmentsActorOrganization)
		require.Truef(t, ok, "got %T, not Organization", actor)
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		response, err = queryWithInterfaceNoFragments(ctx, client, queryWithInterfaceNoFragmentsVariables{
			Id: "4757233945723",
		})
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: null

		assert.Equal(t, "1", response.Viewer.Id)
		assert.Equal(t, "octocat", response.Viewer.Login)

		assert.Nil(t, response.Actor)
	}
}

func TestInterfaceListField(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithInterfaceListField($ids: [ID!]!) {
		actors(ids: $ids) { id login }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListField(ctx, client, queryWithInterfaceListFieldVariables{
			Ids: []string{"1", "3", "12847394823"},
		})
		require.NoError(t, err)

		require.Len(t, response.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion:
		require.NotNil(t, response.Actors[0])
		assert.Equal(t, "User", requirePtrValue(t, (*response.Actors[0]).GetTypename()))
		assert.Equal(t, "1", (*response.Actors[0]).GetId())
		assert.Equal(t, "octocat", (*response.Actors[0]).GetLogin())

		user, ok := (*response.Actors[0]).(*queryWithInterfaceListFieldActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		require.NotNil(t, response.Actors[1])
		assert.Equal(t, "Organization", requirePtrValue(t, (*response.Actors[1]).GetTypename()))
		assert.Equal(t, "3", (*response.Actors[1]).GetId())
		assert.Equal(t, "octo-org", (*response.Actors[1]).GetLogin())

		org, ok := (*response.Actors[1]).(*queryWithInterfaceListFieldActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", *response.Actors[1])
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		assert.Nil(t, response.Actors[2])
	}
}

func TestInterfaceListPointerField(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithInterfaceListPointerField($ids: [ID!]!) {
		# @octoqlgen(pointer: true)
		actors(ids: $ids) {
			__typename id login
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListPointerField(ctx, client, queryWithInterfaceListPointerFieldVariables{
			Ids: []string{"1", "3", "12847394823"},
		})
		require.NoError(t, err)

		require.Len(t, response.Actors, 3)

		// Check fields both via interface and via type-assertion:
		require.NotNil(t, response.Actors[0])
		assert.Equal(t, "User", requirePtrValue(t, (*response.Actors[0]).GetTypename()))
		assert.Equal(t, "1", (*response.Actors[0]).GetId())
		assert.Equal(t, "octocat", (*response.Actors[0]).GetLogin())

		user, ok := (*response.Actors[0]).(*queryWithInterfaceListPointerFieldActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		require.NotNil(t, response.Actors[1])
		assert.Equal(t, "Organization", requirePtrValue(t, (*response.Actors[1]).GetTypename()))
		assert.Equal(t, "3", (*response.Actors[1]).GetId())
		assert.Equal(t, "octo-org", (*response.Actors[1]).GetLogin())

		org, ok := (*response.Actors[1]).(*queryWithInterfaceListPointerFieldActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Actors[1])
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		assert.Nil(t, response.Actors[2])
	}
}

func TestFragments(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithFragments($ids: [ID!]!) {
		actors(ids: $ids) {
			__typename id
			... on Actor { id login }
			... on Organization {
				id
				plan { name }
				topContributor {
					id
					... on Actor { login }
					... on User { contributionCount }
				}
			}
			... on RepositoryOwner { contributionCount }
			... on User { status { emoji } }
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithFragments(ctx, client, queryWithFragmentsVariables{
			Ids: []string{"1", "3", "12847394823"},
		})
		require.NoError(t, err)

		require.Len(t, response.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id login contributionCount.
		require.NotNil(t, response.Actors[0])
		assert.Equal(t, "User", requirePtrValue(t, (*response.Actors[0]).GetTypename()))
		assert.Equal(t, "1", (*response.Actors[0]).GetId())
		assert.Equal(t, "octocat", (*response.Actors[0]).GetLogin())
		// (status and contributionCount we need to cast for)

		user, ok := (*response.Actors[0]).(*queryWithFragmentsActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		require.NotNil(t, user.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(t, user.Status.Emoji))
		assert.Equal(t, 17, requirePtrValue(t, user.ContributionCount))

		// Organization has, in total, the fields:
		//	__typename
		//	id
		//	plan
		//	contributionCount
		//	topContributor {
		//		id
		//		login
		//		... on User { contributionCount }
		//	}
		require.NotNil(t, response.Actors[1])
		assert.Equal(t, "Organization", requirePtrValue(t, (*response.Actors[1]).GetTypename()))
		assert.Equal(t, "3", (*response.Actors[1]).GetId())
		// (plan, contributionCount, and topContributor.* we have to cast for)

		org, ok := (*response.Actors[1]).(*queryWithFragmentsActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", *response.Actors[1])
		assert.Equal(t, "3", org.Id)
		require.NotNil(t, org.Plan)
		assert.Equal(t, gqlserver.PlanNameTeam, org.Plan.Name)

		require.NotNil(t, org.TopContributor)
		assert.Equal(t, "1", (*org.TopContributor).GetId())
		assert.Equal(t, "octocat", (*org.TopContributor).GetLogin())
		// (contributionCount we have to cast for, again)

		topContributor, ok := (*org.TopContributor).(*queryWithFragmentsActorsOrganizationTopContributorUser)
		require.Truef(t, ok, "got %T, not User", *org.TopContributor)
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "octocat", topContributor.Login)
		assert.Equal(t, 17, requirePtrValue(t, topContributor.ContributionCount))

		assert.Nil(t, response.Actors[2])
	}
}

func TestNamedFragments(t *testing.T) {
	_ = `# @octoqlgen
	fragment organizationFields on Organization {
		id
		plan { name }
		topContributor { id ...userFields ...repositoryOwnerFields }
	}

	fragment moreUserFields on User {
		id
		status { emoji }
	}

	fragment repositoryOwnerFields on RepositoryOwner {
		...moreUserFields
		contributionCount
	}
	
	fragment userFields on User {
		id
		...repositoryOwnerFields
		...moreUserFields
	}

	query queryWithNamedFragments($ids: [ID!]!) {
		actors(ids: $ids) {
			__typename id
			...organizationFields
			...userFields
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithNamedFragments(ctx, client, queryWithNamedFragmentsVariables{
			Ids: []string{"1", "3", "12847394823"},
		})
		require.NoError(t, err)

		require.Len(t, response.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id contributionCount.
		require.NotNil(t, response.Actors[0])
		assert.Equal(t, "User", requirePtrValue(t, (*response.Actors[0]).GetTypename()))
		assert.Equal(t, "1", (*response.Actors[0]).GetId())
		// (contributionCount, status we need to cast for)

		user, ok := (*response.Actors[0]).(*queryWithNamedFragmentsActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "1", user.userFields.Id)
		assert.Equal(t, "1", user.userFields.moreUserFields.Id)
		assert.Equal(t, "1", user.userFields.repositoryOwnerFieldsUser.moreUserFields.Id)
		// on UserFields, but we should be able to access directly via embedding:
		assert.Equal(t, 17, requirePtrValue(t, user.ContributionCount))
		require.NotNil(t, user.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(t, user.Status.Emoji))
		require.NotNil(t, user.userFields.moreUserFields.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(t, user.userFields.moreUserFields.Status.Emoji))
		require.NotNil(t, user.userFields.repositoryOwnerFieldsUser.moreUserFields.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(
			t,
			user.userFields.repositoryOwnerFieldsUser.moreUserFields.Status.Emoji,
		))

		// Organization has, in total, the fields:
		//	__typename
		//	id
		//	plan
		//	topContributor { id contributionCount }
		require.NotNil(t, response.Actors[1])
		assert.Equal(t, "Organization", requirePtrValue(t, (*response.Actors[1]).GetTypename()))
		assert.Equal(t, "3", (*response.Actors[1]).GetId())
		// (plan.* and topContributor.* we have to cast for)

		org, ok := (*response.Actors[1]).(*queryWithNamedFragmentsActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", *response.Actors[1])
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "3", org.organizationFields.Id)
		// on OrganizationFields:
		require.NotNil(t, org.Plan)
		assert.Equal(t, gqlserver.PlanNameTeam, org.Plan.Name)
		require.NotNil(t, org.TopContributor)
		assert.Equal(t, "1", (*org.TopContributor).GetId())
		// (contributionCount we have to cast for, again)

		topContributor, ok := (*org.TopContributor).(*organizationFieldsTopContributorUser)
		require.Truef(t, ok, "got %T, not User", *org.TopContributor)
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "1", topContributor.userFields.Id)
		assert.Equal(t, "1", topContributor.userFields.moreUserFields.Id)
		assert.Equal(t, "1", topContributor.userFields.repositoryOwnerFieldsUser.moreUserFields.Id)
		// on UserFields:
		assert.Equal(t, 17, requirePtrValue(t, topContributor.ContributionCount))
		require.NotNil(t, topContributor.userFields.moreUserFields.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(t, topContributor.userFields.moreUserFields.Status.Emoji))
		require.NotNil(t, topContributor.userFields.repositoryOwnerFieldsUser.moreUserFields.Status)
		assert.Equal(t, ":octocat:", requirePtrValue(
			t,
			topContributor.userFields.repositoryOwnerFieldsUser.moreUserFields.Status.Emoji,
		))

		// RepositoryOwner-based fields we can also get by casting to the fragment-interface.
		repoOwnerTopContributor, ok := (*org.TopContributor).(repositoryOwnerFields)
		require.Truef(t, ok, "got %T, not RepositoryOwner", *org.TopContributor)
		assert.Equal(t, 17, requirePtrValue(t, repoOwnerTopContributor.GetContributionCount()))

		assert.Nil(t, response.Actors[2])
	}
}

func TestFlatten(t *testing.T) {
	_ = `# @octoqlgen
	# @octoqlgen(flatten: true)
	fragment actorFields on Actor {
		...innerActorFields
	}

	fragment innerActorFields on Actor {
		id
		login
		... on User {
			# @octoqlgen(flatten: true)
			repositories {
				...repositoriesFields
			}
		}
	}

	fragment repositoriesFields on Repository {
		id
		name
	}

	# @octoqlgen(flatten: true)
	fragment flattenedUserFields on User {
		...flattenedRepositoryOwnerFields
	}

	# @octoqlgen(flatten: true)
	fragment flattenedRepositoryOwnerFields on RepositoryOwner {
		...innerRepositoryOwnerFields
	}

	fragment innerRepositoryOwnerFields on RepositoryOwner {
		contributionCount
	}
	
	fragment queryFragment on Query {
		actors(ids: $ids) {
			__typename id
			...flattenedUserFields
			... on Organization {
				# @octoqlgen(flatten: true)
				topContributor {
					...actorFields
				}
			}
		}
	}

	# @octoqlgen(flatten: true)
	query queryWithFlatten(
		$ids: [ID!]!,
	) {
		...queryFragment
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithFlatten(ctx, client, queryWithFlattenVariables{
			Ids: []string{"1", "3", "12847394823"},
		})
		require.NoError(t, err)

		require.Len(t, response.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id contributionCount.
		require.NotNil(t, response.Actors[0])
		assert.Equal(t, "User", requirePtrValue(t, (*response.Actors[0]).GetTypename()))
		assert.Equal(t, "1", (*response.Actors[0]).GetId())
		// (contributionCount we need to cast for)

		user, ok := (*response.Actors[0]).(*queryFragmentActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, 17, requirePtrValue(t, user.innerRepositoryOwnerFieldsUser.ContributionCount))

		// Organization has, in total, the fields:
		//	__typename
		//	id
		//	topContributor { id login ... on User { repositories { id name } } }
		require.NotNil(t, response.Actors[1])
		assert.Equal(t, "Organization", requirePtrValue(t, (*response.Actors[1]).GetTypename()))
		assert.Equal(t, "3", (*response.Actors[1]).GetId())
		// (topContributor.* we have to cast for)

		org, ok := (*response.Actors[1]).(*queryFragmentActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", *response.Actors[1])
		assert.Equal(t, "3", org.Id)
		// on ActorFields:
		require.NotNil(t, org.TopContributor)
		assert.Equal(t, "1", (*org.TopContributor).GetId())
		assert.Equal(t, "octocat", (*org.TopContributor).GetLogin())
		// (repositories.* we have to cast for, again)

		topContributor, ok := (*org.TopContributor).(*innerActorFieldsUser)
		require.Truef(t, ok, "got %T, not User", *org.TopContributor)
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "octocat", topContributor.Login)
		assert.Len(t, topContributor.Repositories, 1)
		assert.Equal(t, "10", topContributor.Repositories[0].Id)
		assert.Equal(t, "octo-repo", topContributor.Repositories[0].Name)

		assert.Nil(t, response.Actors[2])
	}
}

func TestSearch(t *testing.T) {
	_ = `# @octoqlgen
	query queryWithSearch($query: String!, $searchType: SearchType!) {
		search(query: $query, type: $searchType) {
			__typename
			... on Node { id }
			... on Repository { name stargazerCount }
			... on Issue { title issueState: state }
			... on PullRequest { title pullRequestState: state }
			... on Actor { login }
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithSearch(ctx, client, queryWithSearchVariables{
			Query:      "octo",
			SearchType: gqlserver.SearchTypeRepository,
		})
		require.NoError(t, err)
		require.Len(t, response.Search, 1)

		repo, ok := response.Search[0].(*queryWithSearchSearchRepository)
		require.Truef(t, ok, "got %T, not Repository", response.Search[0])
		assert.Equal(t, "octo-repo", repo.Name)
		assert.Equal(t, 42, repo.StargazerCount)

		response, err = queryWithSearch(ctx, client, queryWithSearchVariables{
			Query:      "bug",
			SearchType: gqlserver.SearchTypeIssue,
		})
		require.NoError(t, err)
		require.Len(t, response.Search, 2)

		issue, ok := response.Search[0].(*queryWithSearchSearchIssue)
		require.Truef(t, ok, "got %T, not Issue", response.Search[0])
		assert.Equal(t, "Bug report", issue.Title)
		assert.Equal(t, gqlserver.IssueStateOpen, issue.IssueState)

		pr, ok := response.Search[1].(*queryWithSearchSearchPullRequest)
		require.Truef(t, ok, "got %T, not PullRequest", response.Search[1])
		assert.Equal(t, "Fix bug", pr.Title)
		assert.Equal(t, gqlserver.PullRequestStateMerged, pr.PullRequestState)

		response, err = queryWithSearch(ctx, client, queryWithSearchVariables{
			Query:      "dependabot",
			SearchType: gqlserver.SearchTypeUser,
		})
		require.NoError(t, err)
		require.Len(t, response.Search, 1)

		bot, ok := response.Search[0].(*queryWithSearchSearchBot)
		require.Truef(t, ok, "got %T, not Bot", response.Search[0])
		assert.Equal(t, "dependabot", bot.Login)
	}
}

func ptr[T any](value T) *T {
	return &value
}

func requirePtrValue[T any](t *testing.T, value *T) T {
	t.Helper()
	require.NotNil(t, value)
	return *value
}

func TestGeneratedCode(t *testing.T) {
	omit := false
	runGenerateTest(t, &generate.Config{
		Schema:                          generate.StringList{"internal/integration/schema.graphql"},
		Operations:                      generate.StringList{"internal/integration/*_test.go"},
		Generated:                       "internal/integration/generated.go",
		OmitUnreferencedImplementations: &omit,
		Bindings: map[string]*generate.TypeBinding{
			"AddCommentInput": {
				Type: "github.com/willabides/octoql/internal/integration/server.AddCommentInput",
			},
			"AddStarInput": {
				Type: "github.com/willabides/octoql/internal/integration/server.AddStarInput",
			},
			"Date": {
				Type:        "time.Time",
				Marshaler:   "github.com/willabides/octoql/internal/testutil.MarshalDate",
				Unmarshaler: "github.com/willabides/octoql/internal/testutil.UnmarshalDate",
			},
			"IssueState": {
				Type: "github.com/willabides/octoql/internal/integration/server.IssueState",
			},
			"PlanName": {
				Type: "github.com/willabides/octoql/internal/integration/server.PlanName",
			},
			"PullRequestState": {
				Type: "github.com/willabides/octoql/internal/integration/server.PullRequestState",
			},
			"RemoveStarInput": {
				Type: "github.com/willabides/octoql/internal/integration/server.RemoveStarInput",
			},
			"SearchType": {
				Type: "github.com/willabides/octoql/internal/integration/server.SearchType",
			},
		},
	})
}

//go:generate go run github.com/willabides/octoql/cmd/octoqlgen generate --config octoqlgen.yaml
