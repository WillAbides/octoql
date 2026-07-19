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
	"github.com/willabides/octoql/internal/integration/server"
)

func TestSimpleQuery(t *testing.T) {
	_ = `# @genqlient
	query simpleQuery { me { id name luckyNumber greatScalar } }`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := simpleQuery(ctx, client)
		require.NoError(t, err)

		assert.Equal(t, "1", response.Data.Me.Id)
		assert.Equal(t, "Yours Truly", response.Data.Me.Name)
		assert.Equal(t, 17, response.Data.Me.LuckyNumber)
	}
}

func TestMutation(t *testing.T) {
	_ = `# @genqlient
	mutation createUser($user: NewUser!) { createUser(input: $user) { id name } }`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	postClient := newRoundtripClient(server.URL)

	response, err := createUser(ctx, postClient, NewUser{Name: "Jack"})
	require.NoError(t, err)
	assert.Equal(t, "5", response.Data.CreateUser.Id)
	assert.Equal(t, "Jack", response.Data.CreateUser.Name)
}

func TestServerError(t *testing.T) {
	_ = `# @genqlient
	query failingQuery { fail me { id } }`

	ctx := context.Background()
	server := server.RunServer()
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
		assert.Equal(t, "1", response.Data.Me.Id)
	}
}

func TestNetworkError(t *testing.T) {
	ctx := context.Background()
	clients := newRoundtripClients("https://nothing.invalid/graphql")

	for _, client := range clients {
		response, err := failingQuery(ctx, client)
		assert.Error(t, err)
		var gqlErrors octoql.Errors
		assert.False(t, errors.As(err, &gqlErrors), "network errors should not contain octoql.Errors")
		assert.Nil(t, response)
	}
}

func TestVariables(t *testing.T) {
	_ = `# @genqlient
	query queryWithVariables($id: ID!) { user(id: $id) { id name luckyNumber } }`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := []*octoql.Client{octoql.NewClient(server.URL, http.DefaultClient)}

	for _, client := range clients {
		response, err := queryWithVariables(ctx, client, "2")
		require.NoError(t, err)

		assert.Equal(t, "2", response.Data.User.Id)
		assert.Equal(t, "Raven", response.Data.User.Name)
		assert.Equal(t, -1, response.Data.User.LuckyNumber)

		response, err = queryWithVariables(ctx, client, "374892379482379")
		require.NoError(t, err)

		assert.Zero(t, response.Data.User)
	}
}

func TestExtensions(t *testing.T) {
	_ = `# @genqlient
	query simpleQueryExt { me { id name luckyNumber } }`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := simpleQueryExt(ctx, client)
		require.NoError(t, err)
		assert.NotNil(t, response.Extensions)
		assert.Equal(t, response.Extensions["foobar"], "test")
	}
}

func TestOmitempty(t *testing.T) {
	_ = `# @genqlient(omitempty: true)
	query queryWithOmitempty($id: ID) {
		user(id: $id) { id name luckyNumber }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithOmitempty(ctx, client, "2")
		require.NoError(t, err)

		assert.Equal(t, "2", response.Data.User.Id)
		assert.Equal(t, "Raven", response.Data.User.Name)
		assert.Equal(t, -1, response.Data.User.LuckyNumber)

		// should return default user, not the user with ID ""
		response, err = queryWithOmitempty(ctx, client, "")
		require.NoError(t, err)

		assert.Equal(t, "1", response.Data.User.Id)
		assert.Equal(t, "Yours Truly", response.Data.User.Name)
		assert.Equal(t, 17, response.Data.User.LuckyNumber)
	}
}

func TestCustomMarshal(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshal($date: Date!) {
		usersBornOn(date: $date) { id name birthdate }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshal(ctx, client,
			time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC))
		require.NoError(t, err)

		assert.Len(t, response.Data.UsersBornOn, 1)
		user := response.Data.UsersBornOn[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.Birthdate)

		response, err = queryWithCustomMarshal(ctx, client,
			time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC))
		require.NoError(t, err)
		assert.Len(t, response.Data.UsersBornOn, 0)
	}
}

func TestCustomMarshalSlice(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshalSlice($dates: [Date!]!) {
		usersBornOnDates(dates: $dates) { id name birthdate }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithCustomMarshalSlice(ctx, client,
			[]time.Time{time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC)})
		require.NoError(t, err)

		assert.Len(t, response.Data.UsersBornOnDates, 1)
		user := response.Data.UsersBornOnDates[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.Birthdate)

		response, err = queryWithCustomMarshalSlice(ctx, client,
			[]time.Time{time.Date(2021, time.January, 1, 12, 34, 56, 789, time.UTC)})
		require.NoError(t, err)
		assert.Len(t, response.Data.UsersBornOnDates, 0)
	}
}

func TestCustomMarshalOptional(t *testing.T) {
	_ = `# @genqlient
	query queryWithCustomMarshalOptional(
		# @genqlient(pointer: true)
		$date: Date,
		# @genqlient(pointer: true)
		$id: ID,
	) {
		userSearch(birthdate: $date, id: $id) { id name birthdate }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		date := time.Date(2025, time.January, 1, 12, 34, 56, 789, time.UTC)
		response, err := queryWithCustomMarshalOptional(ctx, client, &date, nil)
		require.NoError(t, err)

		assert.Len(t, response.Data.UserSearch, 1)
		user := response.Data.UserSearch[0]
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)
		assert.Equal(t,
			time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
			user.Birthdate)

		id := "2"
		response, err = queryWithCustomMarshalOptional(ctx, client, nil, &id)
		require.NoError(t, err)
		assert.Len(t, response.Data.UserSearch, 1)
		user = response.Data.UserSearch[0]
		assert.Equal(t, "2", user.Id)
		assert.Equal(t, "Raven", user.Name)
		assert.Zero(t, user.Birthdate)
	}
}

func TestInterfaceNoFragments(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceNoFragments($id: ID!) {
		being(id: $id) { id name }
		me { id name }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceNoFragments(ctx, client, "1")
		require.NoError(t, err)

		// We should get the following response:
		//	me: User{Id: 1, Name: "Yours Truly"},
		//	being: User{Id: 1, Name: "Yours Truly"},

		assert.Equal(t, "1", response.Data.Me.Id)
		assert.Equal(t, "Yours Truly", response.Data.Me.Name)

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", response.Data.Being.GetTypename())
		assert.Equal(t, "1", response.Data.Being.GetId())
		assert.Equal(t, "Yours Truly", response.Data.Being.GetName())

		user, ok := response.Data.Being.(*queryWithInterfaceNoFragmentsBeingUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Being)
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)

		response, err = queryWithInterfaceNoFragments(ctx, client, "3")
		require.NoError(t, err)

		// We should get the following response:
		//	me: User{Id: 1, Name: "Yours Truly"},
		//	being: Animal{Id: 3, Name: "Fido"},

		assert.Equal(t, "1", response.Data.Me.Id)
		assert.Equal(t, "Yours Truly", response.Data.Me.Name)

		assert.Equal(t, "Animal", response.Data.Being.GetTypename())
		assert.Equal(t, "3", response.Data.Being.GetId())
		assert.Equal(t, "Fido", response.Data.Being.GetName())

		animal, ok := response.Data.Being.(*queryWithInterfaceNoFragmentsBeingAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Being)
		assert.Equal(t, "3", animal.Id)
		assert.Equal(t, "Fido", animal.Name)

		response, err = queryWithInterfaceNoFragments(ctx, client, "4757233945723")
		require.NoError(t, err)

		// We should get the following response:
		//	me: User{Id: 1, Name: "Yours Truly"},
		//	being: null

		assert.Equal(t, "1", response.Data.Me.Id)
		assert.Equal(t, "Yours Truly", response.Data.Me.Name)

		assert.Nil(t, response.Data.Being)
	}
}

func TestInterfaceListField(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceListField($ids: [ID!]!) {
		beings(ids: $ids) { id name }
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListField(ctx, client,
			[]string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Beings, 3)

		// We should get the following three beings:
		//	User{Id: 1, Name: "Yours Truly"},
		//	Animal{Id: 3, Name: "Fido"},
		//	null

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", response.Data.Beings[0].GetTypename())
		assert.Equal(t, "1", response.Data.Beings[0].GetId())
		assert.Equal(t, "Yours Truly", response.Data.Beings[0].GetName())

		user, ok := response.Data.Beings[0].(*queryWithInterfaceListFieldBeingsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Beings[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)

		assert.Equal(t, "Animal", response.Data.Beings[1].GetTypename())
		assert.Equal(t, "3", response.Data.Beings[1].GetId())
		assert.Equal(t, "Fido", response.Data.Beings[1].GetName())

		animal, ok := response.Data.Beings[1].(*queryWithInterfaceListFieldBeingsAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Beings[1])
		assert.Equal(t, "3", animal.Id)
		assert.Equal(t, "Fido", animal.Name)

		assert.Nil(t, response.Data.Beings[2])
	}
}

func TestInterfaceListPointerField(t *testing.T) {
	_ = `# @genqlient
	query queryWithInterfaceListPointerField($ids: [ID!]!) {
		# @genqlient(pointer: true)
		beings(ids: $ids) {
			__typename id name
		}
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithInterfaceListPointerField(ctx, client,
			[]string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Beings, 3)

		// Check fields both via interface and via type-assertion:
		assert.Equal(t, "User", (*response.Data.Beings[0]).GetTypename())
		assert.Equal(t, "1", (*response.Data.Beings[0]).GetId())
		assert.Equal(t, "Yours Truly", (*response.Data.Beings[0]).GetName())

		user, ok := (*response.Data.Beings[0]).(*queryWithInterfaceListPointerFieldBeingsUser)
		require.Truef(t, ok, "got %T, not User", *response.Data.Beings[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)

		assert.Equal(t, "Animal", (*response.Data.Beings[1]).GetTypename())
		assert.Equal(t, "3", (*response.Data.Beings[1]).GetId())
		assert.Equal(t, "Fido", (*response.Data.Beings[1]).GetName())

		animal, ok := (*response.Data.Beings[1]).(*queryWithInterfaceListPointerFieldBeingsAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Beings[1])
		assert.Equal(t, "3", animal.Id)
		assert.Equal(t, "Fido", animal.Name)

		assert.Nil(t, response.Data.Beings[2])
	}
}

func TestFragments(t *testing.T) {
	_ = `# @genqlient
	query queryWithFragments($ids: [ID!]!) {
		beings(ids: $ids) {
			__typename id
			... on Being { id name }
			... on Animal {
				id
				hair { hasHair }
				species
				owner {
					id
					... on Being { name }
					... on User { luckyNumber }
				}
			}
			... on Lucky { luckyNumber }
			... on User { hair { color } }
		}
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithFragments(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Beings, 3)

		// We should get the following three beings:
		//	User{Id: 1, Name: "Yours Truly"},
		//	Animal{Id: 3, Name: "Fido"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id name luckyNumber.
		assert.Equal(t, "User", response.Data.Beings[0].GetTypename())
		assert.Equal(t, "1", response.Data.Beings[0].GetId())
		assert.Equal(t, "Yours Truly", response.Data.Beings[0].GetName())
		// (hair and luckyNumber we need to cast for)

		user, ok := response.Data.Beings[0].(*queryWithFragmentsBeingsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Beings[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "Yours Truly", user.Name)
		assert.Equal(t, "Black", user.Hair.Color)
		assert.Equal(t, 17, user.LuckyNumber)

		// Animal has, in total, the fields:
		//	__typename
		//	id
		//	species
		//	owner {
		//		id
		//		name
		//		... on User { luckyNumber }
		//	}
		assert.Equal(t, "Animal", response.Data.Beings[1].GetTypename())
		assert.Equal(t, "3", response.Data.Beings[1].GetId())
		// (hair, species, and owner.* we have to cast for)

		animal, ok := response.Data.Beings[1].(*queryWithFragmentsBeingsAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Beings[1])
		assert.Equal(t, "3", animal.Id)
		assert.Equal(t, SpeciesDog, animal.Species)
		assert.True(t, animal.Hair.HasHair)

		assert.Equal(t, "1", animal.Owner.GetId())
		assert.Equal(t, "Yours Truly", animal.Owner.GetName())
		// (luckyNumber we have to cast for, again)

		owner, ok := animal.Owner.(*queryWithFragmentsBeingsAnimalOwnerUser)
		require.Truef(t, ok, "got %T, not User", animal.Owner)
		assert.Equal(t, "1", owner.Id)
		assert.Equal(t, "Yours Truly", owner.Name)
		assert.Equal(t, 17, owner.LuckyNumber)

		assert.Nil(t, response.Data.Beings[2])
	}
}

func TestNamedFragments(t *testing.T) {
	_ = `# @genqlient
	fragment AnimalFields on Animal {
		id
		hair { hasHair }
		owner { id ...UserFields ...LuckyFields }
	}

	fragment MoreUserFields on User {
		id
		hair { color }
	}

	fragment LuckyFields on Lucky {
		...MoreUserFields
		luckyNumber
	}
	
	fragment UserFields on User {
		id
		...LuckyFields
		...MoreUserFields
	}

	query queryWithNamedFragments($ids: [ID!]!) {
		beings(ids: $ids) {
			__typename id
			...AnimalFields
			...UserFields
		}
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithNamedFragments(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Beings, 3)

		// We should get the following three beings:
		//	User{Id: 1, Name: "Yours Truly"},
		//	Animal{Id: 3, Name: "Fido"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id luckyNumber.
		assert.Equal(t, "User", response.Data.Beings[0].GetTypename())
		assert.Equal(t, "1", response.Data.Beings[0].GetId())
		// (luckyNumber, hair we need to cast for)

		user, ok := response.Data.Beings[0].(*queryWithNamedFragmentsBeingsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Beings[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, "1", user.UserFields.Id)
		assert.Equal(t, "1", user.UserFields.MoreUserFields.Id)
		assert.Equal(t, "1", user.UserFields.LuckyFieldsUser.MoreUserFields.Id)
		// on UserFields, but we should be able to access directly via embedding:
		assert.Equal(t, 17, user.LuckyNumber)
		assert.Equal(t, "Black", user.Hair.Color)
		assert.Equal(t, "Black", user.UserFields.MoreUserFields.Hair.Color)
		assert.Equal(t, "Black", user.UserFields.LuckyFieldsUser.MoreUserFields.Hair.Color)

		// Animal has, in total, the fields:
		//	__typename
		//	id
		//	hair { hasHair }
		//	owner { id luckyNumber }
		assert.Equal(t, "Animal", response.Data.Beings[1].GetTypename())
		assert.Equal(t, "3", response.Data.Beings[1].GetId())
		// (hair.* and owner.* we have to cast for)

		animal, ok := response.Data.Beings[1].(*queryWithNamedFragmentsBeingsAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Beings[1])
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "3", animal.Id)
		assert.Equal(t, "3", animal.AnimalFields.Id)
		// on AnimalFields:
		assert.True(t, animal.Hair.HasHair)
		assert.Equal(t, "1", animal.Owner.GetId())
		// (luckyNumber we have to cast for, again)

		owner, ok := animal.Owner.(*AnimalFieldsOwnerUser)
		require.Truef(t, ok, "got %T, not User", animal.Owner)
		// Check that we filled in *both* ID fields:
		assert.Equal(t, "1", owner.Id)
		assert.Equal(t, "1", owner.UserFields.Id)
		assert.Equal(t, "1", owner.UserFields.MoreUserFields.Id)
		assert.Equal(t, "1", owner.UserFields.LuckyFieldsUser.MoreUserFields.Id)
		// on UserFields:
		assert.Equal(t, 17, owner.LuckyNumber)
		assert.Equal(t, "Black", owner.UserFields.MoreUserFields.Hair.Color)
		assert.Equal(t, "Black", owner.UserFields.LuckyFieldsUser.MoreUserFields.Hair.Color)

		// Lucky-based fields we can also get by casting to the fragment-interface.
		luckyOwner, ok := animal.Owner.(LuckyFields)
		require.Truef(t, ok, "got %T, not Lucky", animal.Owner)
		assert.Equal(t, 17, luckyOwner.GetLuckyNumber())

		assert.Nil(t, response.Data.Beings[2])
	}
}

func TestFlatten(t *testing.T) {
	_ = `# @genqlient
	# @genqlient(flatten: true)
	fragment BeingFields on Being {
		...InnerBeingFields
	}

	fragment InnerBeingFields on Being {
		id
		name
		... on User {
			# @genqlient(flatten: true)
			friends {
				...FriendsFields
			}
		}
	}

	fragment FriendsFields on User {
		id
		name
	}

	# @genqlient(flatten: true)
	fragment FlattenedUserFields on User {
		...FlattenedLuckyFields
	}

	# @genqlient(flatten: true)
	fragment FlattenedLuckyFields on Lucky {
		...InnerLuckyFields
	}

	fragment InnerLuckyFields on Lucky {
		luckyNumber
	}
	
	fragment QueryFragment on Query {
		beings(ids: $ids) {
			__typename id
			...FlattenedUserFields
			... on Animal {
				# @genqlient(flatten: true)
				owner {
					...BeingFields
				}
			}
		}
	}

	# @genqlient(flatten: true)
	query queryWithFlatten(
		$ids: [ID!]!,
	) {
		...QueryFragment
	}`

	ctx := context.Background()
	server := server.RunServer()
	defer server.Close()
	clients := newRoundtripClients(server.URL)

	for _, client := range clients {
		response, err := queryWithFlatten(ctx, client, []string{"1", "3", "12847394823"})
		require.NoError(t, err)

		require.Len(t, response.Data.Beings, 3)

		// We should get the following three beings:
		//	User{Id: 1, Name: "Yours Truly"},
		//	Animal{Id: 3, Name: "Fido"},
		//	null

		// Check fields both via interface and via type-assertion when possible
		// User has, in total, the fields: __typename id luckyNumber.
		assert.Equal(t, "User", response.Data.Beings[0].GetTypename())
		assert.Equal(t, "1", response.Data.Beings[0].GetId())
		// (luckyNumber we need to cast for)

		user, ok := response.Data.Beings[0].(*QueryFragmentBeingsUser)
		require.Truef(t, ok, "got %T, not User", response.Data.Beings[0])
		assert.Equal(t, "1", user.Id)
		assert.Equal(t, 17, user.InnerLuckyFieldsUser.LuckyNumber)

		// Animal has, in total, the fields:
		//	__typename
		//	id
		//	owner { id name ... on User { friends { id name } } }
		assert.Equal(t, "Animal", response.Data.Beings[1].GetTypename())
		assert.Equal(t, "3", response.Data.Beings[1].GetId())
		// (owner.* we have to cast for)

		animal, ok := response.Data.Beings[1].(*QueryFragmentBeingsAnimal)
		require.Truef(t, ok, "got %T, not Animal", response.Data.Beings[1])
		assert.Equal(t, "3", animal.Id)
		// on AnimalFields:
		assert.Equal(t, "1", animal.Owner.GetId())
		assert.Equal(t, "Yours Truly", animal.Owner.GetName())
		// (friends.* we have to cast for, again)

		owner, ok := animal.Owner.(*InnerBeingFieldsUser)
		require.Truef(t, ok, "got %T, not User", animal.Owner)
		assert.Equal(t, "1", owner.Id)
		assert.Equal(t, "Yours Truly", owner.Name)
		assert.Len(t, owner.Friends, 1)
		assert.Equal(t, "2", owner.Friends[0].Id)
		assert.Equal(t, "Raven", owner.Friends[0].Name)

		assert.Nil(t, response.Data.Beings[2])
	}
}

func TestGeneratedCode(t *testing.T) {
	RunGenerateTest(t, "internal/integration/genqlient.yaml")
}

//go:generate go run github.com/willabides/octoql/cmd/octoqlgen generate genqlient.yaml
