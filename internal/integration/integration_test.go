// Package integration contains genqlient's integration tests, which run
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
	"github.com/willabides/octoql/generate"
	gqlserver "github.com/willabides/octoql/internal/integration/server"
)

func TestGetRepository(t *testing.T) {
	_ = `# @genqlient
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
		response, err := getRepository(ctx, client, "octocat", "octo-repo")
		require.NoError(t, err)

		require.NotNil(t, response.Data.Repository)
		assert.Equal(t, "10", response.Data.Repository.Id)
		assert.Equal(t, "octo-repo", response.Data.Repository.Name)
		assert.Equal(t, "octocat/octo-repo", response.Data.Repository.NameWithOwner)
		assert.Equal(t, "octocat", response.Data.Repository.Owner.GetLogin())
	}
}

func TestMutation(t *testing.T) {
	_ = `# @genqlient
	mutation addComment($input: AddCommentInput!) {
		addComment(input: $input) { commentEdge { node { id body } } }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	postClient := newRoundtripClient(server.URL)

	response, err := addComment(ctx, postClient, gqlserver.AddCommentInput{
		SubjectID: "20",
		Body:      "Thanks for reporting!",
	})
	require.NoError(t, err)
	require.NotNil(t, response.Data.AddComment.CommentEdge)
	require.NotNil(t, response.Data.AddComment.CommentEdge.Node)
	assert.Equal(t, "Thanks for reporting!", response.Data.AddComment.CommentEdge.Node.Body)
}

func TestStarMutations(t *testing.T) {
	_ = `# @genqlient
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

	starResponse, err := addStar(ctx, postClient, gqlserver.AddStarInput{StarrableID: "10"})
	require.NoError(t, err)
	require.NotNil(t, starResponse.Data.AddStar.Starrable)
	assert.Equal(t, "10", starResponse.Data.AddStar.Starrable.GetId())
	assert.True(t, starResponse.Data.AddStar.Starrable.GetViewerHasStarred())
	assert.Equal(t, 43, starResponse.Data.AddStar.Starrable.GetStargazerCount())

	unstarResponse, err := removeStar(ctx, postClient, gqlserver.RemoveStarInput{StarrableID: "10"})
	require.NoError(t, err)
	require.NotNil(t, unstarResponse.Data.RemoveStar.Starrable)
	assert.Equal(t, "10", unstarResponse.Data.RemoveStar.Starrable.GetId())
	assert.False(t, unstarResponse.Data.RemoveStar.Starrable.GetViewerHasStarred())
	assert.Equal(t, 42, unstarResponse.Data.RemoveStar.Starrable.GetStargazerCount())
}

func TestServerError(t *testing.T) {
	_ = `# @genqlient
	query failingQuery { fail viewer { id } }`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := failingQuery(ctx, client)
		// As long as we get some response back, we should still return a full
		// response -- and indeed in this case it should even have another field
		// (which didn't err) set.
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
		assert.NotNil(t, response)
		assert.Equal(t, "1", response.Data.Viewer.Id)
	}
}

func TestVariables(t *testing.T) {
	_ = `# @genqlient
	query queryWithVariables($login: String!) { user(login: $login) { id login contributionCount } }`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := []*octoql.Client{octoql.NewClient(server.URL, http.DefaultClient)}

	for _, client := range clients {
		response, err := queryWithVariables(ctx, client, "raven")
		require.NoError(t, err)

		assert.Equal(t, "2", response.Data.User.Id)
		assert.Equal(t, "raven", response.Data.User.Login)
		assert.Equal(t, -1, response.Data.User.ContributionCount)

		response, err = queryWithVariables(ctx, client, "definitely-not-a-real-login")
		require.NoError(t, err)

		assert.Zero(t, response.Data.User)
	}
}

func TestOmitempty(t *testing.T) {
	_ = `# @genqlient(omitempty: true)
	query queryWithOmitempty($login: String) {
		user(login: $login) { id login contributionCount }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithOmitempty(ctx, client, "raven")
		require.NoError(t, err)

		assert.Equal(t, "2", response.Data.User.Id)
		assert.Equal(t, "raven", response.Data.User.Login)
		assert.Equal(t, -1, response.Data.User.ContributionCount)

		// should return the default viewer-like user, not the user with login ""
		response, err = queryWithOmitempty(ctx, client, "")
		require.NoError(t, err)

		assert.Equal(t, "1", response.Data.User.Id)
		assert.Equal(t, "octocat", response.Data.User.Login)
		assert.Equal(t, 17, response.Data.User.ContributionCount)
	}
}

func TestCustomMarshal(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshal($date: Date!) {
		usersCreatedOn(date: $date) { id login createdAt }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshal(ctx, client,
			time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC))
		require.NoError(t, err)

		assert.Len(t, response.Data.UsersCreatedOn, 1)
		user := response.Data.UsersCreatedOn[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.CreatedAt)

		response, err = queryWithCustomMarshal(ctx, client,
			time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC))
		require.NoError(t, err)
		assert.Len(t, response.Data.UsersCreatedOn, 0)
	}
}

func TestCustomMarshalSlice(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshalSlice($dates: [Date!]!) {
		usersCreatedOnDates(dates: $dates) { id login createdAt }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshalSlice(ctx, client,
			[]time.Time{time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC)})
		require.NoError(t, err)

		assert.Len(t, response.Data.UsersCreatedOnDates, 1)
		user := response.Data.UsersCreatedOnDates[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.CreatedAt)

		response, err = queryWithCustomMarshalSlice(ctx, client,
			[]time.Time{time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC)})
		require.NoError(t, err)
		assert.Len(t, response.Data.UsersCreatedOnDates, 0)
	}
}

func TestCustomMarshalOptional(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshalOptional(
		# @genqlient(pointer: true)
		$date: Date,
		# @genqlient(pointer: true)
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
		response, err := queryWithCustomMarshalOptional(ctx, client, &date, nil)
		require.NoError(t, err)

		assert.Len(t, response.Data.UserSearch, 1)
		user := response.Data.UserSearch[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.CreatedAt)

		login := "raven"
		response, err = queryWithCustomMarshalOptional(ctx, client, nil, &login)
		require.NoError(t, err)
		assert.Len(t, response.Data.UserSearch, 1)
		user = response.Data.UserSearch[0]
		assert.Equal(t, "2", user.Id)
		assert.Equal(t, "raven", user.Login)
		assert.Zero(t, user.CreatedAt)
	}
}

func TestInterfaceNoFragments(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceNoFragments($id: ID!) {
		actor(id: $id) { id login }
		viewer { id login }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceNoFragments(ctx, client, "1")
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: User{Id: 1, Login: "octocat"},

		assert.Equal(t, "1", response.Data.Viewer.Id)
		assert.Equal(t, "octocat", response.Data.Viewer.Login)

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", response.Data.Actor.GetTypename())
		assert.Equal(t, "1", response.Data.Actor.GetId())
		assert.Equal(t, "octocat", response.Data.Actor.GetLogin())

		user, ok := response.Data.Actor.(*queryWithInterfaceNoFragmentsActorUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Actor)
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		response, err = queryWithInterfaceNoFragments(ctx, client, "3")
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: Organization{Id: 3, Login: "octo-org"},

		assert.Equal(t, "1", response.Data.Viewer.Id)
		assert.Equal(t, "octocat", response.Data.Viewer.Login)

		assert.Equal(t, "Organization", response.Data.Actor.GetTypename())
		assert.Equal(t, "3", response.Data.Actor.GetId())
		assert.Equal(t, "octo-org", response.Data.Actor.GetLogin())

		org, ok := response.Data.Actor.(*queryWithInterfaceNoFragmentsActorOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actor)
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		response, err = queryWithInterfaceNoFragments(ctx, client, "4757233945723")
		require.NoError(t, err)

		// We should get the following response:
		//	viewer: User{Id: 1, Login: "octocat"},
		//	actor: null

		assert.Equal(t, "1", response.Data.Viewer.Id)
		assert.Equal(t, "octocat", response.Data.Viewer.Login)

		assert.Nil(t, response.Data.Actor)
	}
}

func TestInterfaceListField(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceListField($ids: [ID!]!) {
		actors(ids: $ids) { id login }
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListField(ctx, client,
			[]string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", response.Data.Actors[0].GetTypename())
		assert.Equal(t, "1", response.Data.Actors[0].GetId())
		assert.Equal(t, "octocat", response.Data.Actors[0].GetLogin())

		user, ok := response.Data.Actors[0].(*queryWithInterfaceListFieldActorsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		assert.Equal(t, "Organization", response.Data.Actors[1].GetTypename())
		assert.Equal(t, "3", response.Data.Actors[1].GetId())
		assert.Equal(t, "octo-org", response.Data.Actors[1].GetLogin())

		org, ok := response.Data.Actors[1].(*queryWithInterfaceListFieldActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actors[1])
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		assert.Nil(t, response.Data.Actors[2])
	}
}

func TestInterfaceListPointerField(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceListPointerField($ids: [ID!]!) {
		# @genqlient(pointer: true)
		actors(ids: $ids) {
			__typename id login
		}
	}`

	ctx := context.Background()
	server := gqlserver.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListPointerField(ctx, client,
			[]string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Actors, 3)

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", (*response.Data.Actors[0]).GetTypename())
		assert.Equal(t, "1", (*response.Data.Actors[0]).GetId())
		assert.Equal(t, "octocat", (*response.Data.Actors[0]).GetLogin())

		user, ok := (*response.Data.Actors[0]).(*queryWithInterfaceListPointerFieldActorsUser)
		require.Truef(t, ok, "got %T, not User", *response.Data.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)

		assert.Equal(t, "Organization", (*response.Data.Actors[1]).GetTypename())
		assert.Equal(t, "3", (*response.Data.Actors[1]).GetId())
		assert.Equal(t, "octo-org", (*response.Data.Actors[1]).GetLogin())

		org, ok := (*response.Data.Actors[1]).(*queryWithInterfaceListPointerFieldActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actors[1])
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "octo-org", org.Login)

		assert.Nil(t, response.Data.Actors[2])
	}
}

func TestFragments(t *testing.T) {
	_ = `# @genqlient
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
		response, err := queryWithFragments(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id login contributionCount.
		assert.Equal(t, "User", response.Data.Actors[0].GetTypename())
		assert.Equal(t, "1", response.Data.Actors[0].GetId())
		assert.Equal(t, "octocat", response.Data.Actors[0].GetLogin())
		// (status and contributionCount we need to cast for)

		user, ok := response.Data.Actors[0].(*queryWithFragmentsActorsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "octocat", user.Login)
		assert.Equal(t, ":octocat:", user.Status.Emoji)
		assert.Equal(t, 17, user.ContributionCount)

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
		assert.Equal(t, "Organization", response.Data.Actors[1].GetTypename())
		assert.Equal(t, "3", response.Data.Actors[1].GetId())
		// (plan, contributionCount, and topContributor.* we have to cast for)

		org, ok := response.Data.Actors[1].(*queryWithFragmentsActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actors[1])
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, gqlserver.PlanNameTeam, org.Plan.Name)

		assert.Equal(t, "1", org.TopContributor.GetId())
		assert.Equal(t, "octocat", org.TopContributor.GetLogin())
		// (contributionCount we have to cast for, again)

		topContributor, ok := org.TopContributor.(*queryWithFragmentsActorsOrganizationTopContributorUser)
		require.Truef(t, ok, "got %T, not User", org.TopContributor)
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "octocat", topContributor.Login)
		assert.Equal(t, 17, topContributor.ContributionCount)

		assert.Nil(t, response.Data.Actors[2])
	}
}

func TestNamedFragments(t *testing.T) {
	_ = `# @genqlient
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
		response, err := queryWithNamedFragments(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id contributionCount.
		assert.Equal(t, "User", response.Data.Actors[0].GetTypename())
		assert.Equal(t, "1", response.Data.Actors[0].GetId())
		// (contributionCount, status we need to cast for)

		user, ok := response.Data.Actors[0].(*queryWithNamedFragmentsActorsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "1", user.userFields.Id)
		assert.Equal(t, "1", user.userFields.moreUserFields.Id)
		assert.Equal(t, "1", user.userFields.repositoryOwnerFieldsUser.moreUserFields.Id)
		// on UserFields, but we should be able to access directly via embedding:
		assert.Equal(t, 17, user.ContributionCount)
		assert.Equal(t, ":octocat:", user.Status.Emoji)
		assert.Equal(t, ":octocat:", user.userFields.moreUserFields.Status.Emoji)
		assert.Equal(t, ":octocat:", user.userFields.repositoryOwnerFieldsUser.moreUserFields.Status.Emoji)

		// Organization has, in total, the fields:
		//	__typename
		//	id
		//	plan
		//	topContributor { id contributionCount }
		assert.Equal(t, "Organization", response.Data.Actors[1].GetTypename())
		assert.Equal(t, "3", response.Data.Actors[1].GetId())
		// (plan.* and topContributor.* we have to cast for)

		org, ok := response.Data.Actors[1].(*queryWithNamedFragmentsActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actors[1])
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "3", org.Id)
		assert.Equal(t, "3", org.organizationFields.Id)
		// on OrganizationFields:
		assert.Equal(t, gqlserver.PlanNameTeam, org.Plan.Name)
		assert.Equal(t, "1", org.TopContributor.GetId())
		// (contributionCount we have to cast for, again)

		topContributor, ok := org.TopContributor.(*organizationFieldsTopContributorUser)
		require.Truef(t, ok, "got %T, not User", org.TopContributor)
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "1", topContributor.userFields.Id)
		assert.Equal(t, "1", topContributor.userFields.moreUserFields.Id)
		assert.Equal(t, "1", topContributor.userFields.repositoryOwnerFieldsUser.moreUserFields.Id)
		// on UserFields:
		assert.Equal(t, 17, topContributor.ContributionCount)
		assert.Equal(t, ":octocat:", topContributor.userFields.moreUserFields.Status.Emoji)
		assert.Equal(t, ":octocat:", topContributor.userFields.repositoryOwnerFieldsUser.moreUserFields.Status.Emoji)

		// RepositoryOwner-based fields we can also get by casting to the fragment-interface.
		repoOwnerTopContributor, ok := org.TopContributor.(repositoryOwnerFields)
		require.Truef(t, ok, "got %T, not RepositoryOwner", org.TopContributor)
		assert.Equal(t, 17, repoOwnerTopContributor.GetContributionCount())

		assert.Nil(t, response.Data.Actors[2])
	}
}

func TestFlatten(t *testing.T) {
	_ = `# @genqlient
	# @genqlient(flatten: true)
	fragment actorFields on Actor {
		...innerActorFields
	}

	fragment innerActorFields on Actor {
		id
		login
		... on User {
			# @genqlient(flatten: true)
			repositories {
				...repositoriesFields
			}
		}
	}

	fragment repositoriesFields on Repository {
		id
		name
	}

	# @genqlient(flatten: true)
	fragment flattenedUserFields on User {
		...flattenedRepositoryOwnerFields
	}

	# @genqlient(flatten: true)
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
				# @genqlient(flatten: true)
				topContributor {
					...actorFields
				}
			}
		}
	}

	# @genqlient(flatten: true)
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
		response, err := queryWithFlatten(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Actors, 3)

		// We should get the following three actors:
		//	User{Id: 1, Login: "octocat"},
		//	Organization{Id: 3, Login: "octo-org"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id contributionCount.
		assert.Equal(t, "User", response.Data.Actors[0].GetTypename())
		assert.Equal(t, "1", response.Data.Actors[0].GetId())
		// (contributionCount we need to cast for)

		user, ok := response.Data.Actors[0].(*queryFragmentActorsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Actors[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, 17, user.innerRepositoryOwnerFieldsUser.ContributionCount)

		// Organization has, in total, the fields:
		//	__typename
		//	id
		//	topContributor { id login ... on User { repositories { id name } } }
		assert.Equal(t, "Organization", response.Data.Actors[1].GetTypename())
		assert.Equal(t, "3", response.Data.Actors[1].GetId())
		// (topContributor.* we have to cast for)

		org, ok := response.Data.Actors[1].(*queryFragmentActorsOrganization)
		require.Truef(t, ok, "got %T, not Organization", response.Data.Actors[1])
		assert.Equal(t, "3", org.Id)
		// on ActorFields:
		assert.Equal(t, "1", org.TopContributor.GetId())
		assert.Equal(t, "octocat", org.TopContributor.GetLogin())
		// (repositories.* we have to cast for, again)

		topContributor, ok := org.TopContributor.(*innerActorFieldsUser)
		require.Truef(t, ok, "got %T, not User", org.TopContributor)
		assert.Equal(t, "1", topContributor.Id)
		assert.Equal(t, "octocat", topContributor.Login)
		assert.Len(t, topContributor.Repositories, 1)
		assert.Equal(t, "10", topContributor.Repositories[0].Id)
		assert.Equal(t, "octo-repo", topContributor.Repositories[0].Name)

		assert.Nil(t, response.Data.Actors[2])
	}
}

func TestSearch(t *testing.T) {
	_ = `# @genqlient
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
		response, err := queryWithSearch(ctx, client, "octo", gqlserver.SearchTypeRepository)
		require.NoError(t, err)
		require.Len(t, response.Data.Search, 1)

		repo, ok := response.Data.Search[0].(*queryWithSearchSearchRepository)
		require.Truef(t, ok, "got %T, not Repository", response.Data.Search[0])
		assert.Equal(t, "octo-repo", repo.Name)
		assert.Equal(t, 42, repo.StargazerCount)

		response, err = queryWithSearch(ctx, client, "bug", gqlserver.SearchTypeIssue)
		require.NoError(t, err)
		require.Len(t, response.Data.Search, 2)

		issue, ok := response.Data.Search[0].(*queryWithSearchSearchIssue)
		require.Truef(t, ok, "got %T, not Issue", response.Data.Search[0])
		assert.Equal(t, "Bug report", issue.Title)
		assert.Equal(t, gqlserver.IssueStateOpen, issue.IssueState)

		pr, ok := response.Data.Search[1].(*queryWithSearchSearchPullRequest)
		require.Truef(t, ok, "got %T, not PullRequest", response.Data.Search[1])
		assert.Equal(t, "Fix bug", pr.Title)
		assert.Equal(t, gqlserver.PullRequestStateMerged, pr.PullRequestState)

		response, err = queryWithSearch(ctx, client, "dependabot", gqlserver.SearchTypeUser)
		require.NoError(t, err)
		require.Len(t, response.Data.Search, 1)

		bot, ok := response.Data.Search[0].(*queryWithSearchSearchBot)
		require.Truef(t, ok, "got %T, not Bot", response.Data.Search[0])
		assert.Equal(t, "dependabot", bot.Login)
	}
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
