package parsing_errors

var _ = `# @octoqlgen
	query myBadQuery(varMissingDollar: String) {
	  field(arg: $varMissingDollar)
	}
`
