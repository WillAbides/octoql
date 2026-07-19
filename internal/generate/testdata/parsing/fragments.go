package parsing

const MyFragment = `
	# @octoqlgen
	fragment MyFragment on MyType {
		myFragmentField
		...NestedFragment
	}
`

var _ = `
	# @octoqlgen
	fragment NestedFragment on MyType {
		myOtherFragmentField
	}
`

const MyQuery = `
	# @octoqlgen
	query MyQuery {
		myField
		myOtherField {
		...MyFragment
		}
	}
`

func query(s string) {}

func MyMutation() {
	query(`
		# @octoqlgen
		mutation MyMutation {
			myField
			myOtherField {
			...MyFragment
			}
		}
	`)
}

const (
	NotAString = 1
	NotAQuery  = `query
		writing with GraphQL is fun!`
)
