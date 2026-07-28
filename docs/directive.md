# The `@octoqlgen` directive

The `@octoqlgen` quasi-directive configures octoqlgen for individual
operations, fragments, fields, and arguments.

Its syntax is just like a GraphQL directive, except that it goes in a comment
on the line immediately preceding the element it applies to. This is because
GraphQL expects directives in queries to be defined by the server rather than
the client, so a real `@octoqlgen` directive would be rejected as nonexistent.

`@octoqlgen` is the only supported spelling. There is no compatibility alias.

## Placement

Directives may be applied to fields, arguments, or an entire operation or named
fragment. A directive on the line preceding an operation or a named fragment
applies to all relevant elements within it. Every other directive is directly
attached to the outermost element beginning on the following line. That direct
attachment does not apply to nested elements, including a field's arguments and
selections and an operation's variable definitions. A directive preceding peer
nodes of the same kind at the same nesting depth on one line is rejected; put
those nodes on separate lines. In all cases other comments may appear between
the directive and the element it applies to.
String option values must not be empty. Use `bind: "-"` to explicitly opt out
of a configured binding.

For example:

```graphql
# @octoqlgen(n: "a")

# @octoqlgen(n: "b")
#
# Comment describing the query
#
# @octoqlgen(n: "c")
query MyQuery(arg1: String,
  # @octoqlgen(n: "d")
  arg2: String,
  arg3: MyInput,
  arg4: String,
) {
  # @octoqlgen(n: "e")
  field1
  field2
  # @octoqlgen(n: "f")
  field3(argument: "value") { field4 field5 }
}
```

Here directive `a` is ignored, `b` and `c` apply to all relevant nodes in the
query, `d` applies to `arg2`, `e` applies to `field1`, and `f` applies to
`field3` but not its argument, `field4`, or `field5`.

Except as noted below, directives on nodes take precedence over directives on
the entire operation, so `d`, `e`, and `f` take precedence over `b` and `c`.
Multiple directives on the same node, such as `b` and `c`, must not conflict.

Directly attached directives do *not* apply to their children, so `d` does not
apply to the fields of `MyInput` and `f` does not apply to `field4`. Directives
on operations and fragments do apply to children, so both `b` and `c` apply to
the fields of `MyInput` and to `field4`.

Multiple `@octoqlgen` directives are allowed in the same location as long as
they do not have conflicting options. Directives are valid on queries,
mutations, fields, fragment definitions, and variable definitions. octoql does
not support subscriptions.

## `for`

Treats the entire `@octoqlgen` directive as if it were applied to the named
field of the named type, written as `"MyType.myField"`. It must be applied to
an entire operation or fragment.

This is especially useful for input-type options like `omitempty` and
`pointer`, which are equally meaningful on input-type fields as on arguments,
but have no natural syntax to put them on fields.

For input types, unless the type has the `typename` option set, all operations
and fragments in the same package that use the type should have matching
directives. This avoids needing to give them more complex type names, and is
not currently validated.

Given the following query:

```graphql
# @octoqlgen(for: "MyInput.myField", omitempty: true)
# @octoqlgen(for: "MyInput.myOtherField", pointer: true)
# @octoqlgen(for: "MyOutput.id", bind: "path/to/pkg.MyOutputID")
query MyQuery(
  $arg: MyInput
) { ... }
```

octoqlgen generates:

```go
type MyInput struct {
	MyField      <type>  `json:"myField,omitempty"`
	MyOtherField *<type> `json:"myOtherField"`
	MyThirdField <type>  `json:"myThirdField"`
}
```

and uses it for the argument to `MyQuery`. Similarly, if `MyOutput.id` is ever
requested in the response, it uses the given type.

## `omitempty`

Omits this argument, or input-type field when combined with `for`, if it has an
empty value. Empty is defined the same as in `encoding/json`: false, 0, a nil
pointer, a nil interface value, and any empty array, slice, map, or string.

Given the following query:

```graphql
# @octoqlgen(omitempty: true)
query MyQuery($arg: String) { ... }
```

octoqlgen generates a variables field like:

```go
Arg *string `json:"arg,omitempty"`
```

A nil pointer omits `arg` from the variables object, while a pointer to `""`
includes the empty string. Add `pointer: false` to generate a string field that
omits `""` instead.

Only applicable to arguments of nullable types. Ignored for types with custom
marshalers; see [`bindings.<name>.marshaler`](configuration.md).

## `pointer`

Controls whether a value uses a pointer type in Go.

Nullable named values and nullable list elements use pointer types by default.
Generated abstract interface values are never wrapped in pointers, because a nil
interface already represents GraphQL null. Set this to false on a specific
argument or field to opt out for other types. The `omitempty` setting remains
independent of pointer selection.

A pointer is useful when the value needs to be passed around and copies should
be avoided, or to distinguish between the Go zero value and null.

## `struct`

Uses a struct type in Go for this field, even when it is an interface.

This is useful for a query like:

```graphql
query MyQuery {
  myInterface { myField }
}
```

where only shared fields of an interface are requested. By default octoqlgen
still generates an interface type, for consistency. That is not necessary here:
a struct works just as well because there are no type-specific fields, and
`struct: true` requests one.

This is only allowed when there are no fragments in play, so that all fields are
on the interface type. Adding a fragment later requires removing this option,
and the generated types will change.

## `flatten`

Uses the generated type of a fragment directly. The field's selection must
contain a single fragment spread.

Given a query like:

```graphql
query MyQuery {
  myField {
    ...MyFragment
  }
}
```

octoqlgen generates these types by default:

```go
type MyQueryResponse struct {
	MyField MyQueryMyFieldMyType
}
type MyQueryMyFieldMyType struct {
	MyFragment
}
```

With `flatten`:

```graphql
query MyQuery {
  # @octoqlgen(flatten: true)
  myField {
    ...MyFragment
  }
}
```

octoqlgen uses the fragment type directly:

```go
type MyQueryResponse struct {
	MyField MyFragment
}
```

This is only applicable when the selection contains one fragment spread and the
field type implements the fragment type. An automatically added `__typename`
selection does not prevent flattening.

## `alias`

Uses the provided name as the Go field name, without creating an alias in the
GraphQL query.

Given a query like:

```graphql
query MyQuery {
  # @octoqlgen(alias: "MyGreatName")
  myField
}
```

octoqlgen generates:

```go
type MyQueryResponse struct {
	MyGreatName <type> `json:"myField"`
}
```

This is similar to the GraphQL alias syntax, such as `myGreatName: myField`, but
it only affects the Go field name, not the GraphQL query. This is especially
useful with GraphQL servers that limit the number of aliases a query may use.

## `bind`

Uses the given Go type for this argument or field instead of an
octoqlgen-generated type.

The value is the fully qualified type name to use for the field, for example:

- `time.Time`
- `map[string]interface{}`
- `[]github.com/you/yourpkg/subpkg.MyType`

The type is the type of the whole field. If the GraphQL field has type
`[DateTime]`, that would be:

```graphql
# @octoqlgen(bind: "[]time.Time")
```

That is not required, though. Mapping to some type `DateList` also works, as
long as its `UnmarshalJSON` method accepts a list of datetimes.

Nullability and the `pointer` option apply to the bound type. Nullable fields
default to a pointer to the bound type, `pointer: false` opts out, and
`pointer: true` forces a pointer for a non-null field.

The bound type must be defined elsewhere in your code. To have octoqlgen create
the type definition, use `typename` instead.

This is effectively a local version of the global
[`bindings`](configuration.md) setting and should be used with similar care.
Setting it to `"-"` overrides any such global setting and uses an
octoqlgen-generated type.

## `typename`

Gives the type of this field the given name in Go.

Given the following query:

```graphql
# @octoqlgen(typename: "MyResp")
query MyQuery {
  # @octoqlgen(typename: "User")
  user {
    id
  }
}
```

octoqlgen generates:

```go
type Resp struct {
	User User
}
type User struct {
	Id string
}
```

instead of its usual, more verbose type names.

`typename` also works on basic types, in which case Go creates a type definition
for that basic type:

```graphql
query MyQuery {
  user {
    # @octoqlgen(typename: "NameType")
    name
  }
}
```

generates:

```go
type Resp struct {
	User User
}
type NameType string
type User struct {
	Name NameType
}
```

Compare this to `@octoqlgen(bind: "path/to/pkg.NameType")`, which does something
similar but depends on `NameType` being defined in another package rather than
having octoqlgen define it.

Choose `typename` carefully, because it can create naming conflicts. octoqlgen
complains if the same type name is used in multiple
places, unless they request the exact same fields in the same order, or if a
type name conflicts with an autogenerated one. Such types should also have
matching `@octoqlgen` directives, although this is not currently validated.
Fragments are often easier to use; when a field contains only a fragment spread,
see [`flatten`](#flatten).

Unlike most directives, when applied to an entire operation `typename` affects
the overall response type rather than being propagated down to all child fields,
which would cause conflicts.

To avoid confusion, `typename` may not be combined with local or global
bindings. To use `typename` instead of a global binding, write
`typename: "MyTypeName", bind: "-"`.

## `@skip` and `@include`

`@skip(if:)` and `@include(if:)` are core GraphQL directives, not `@octoqlgen`
options, but they affect the Go types octoqlgen generates, so they are described
here.

A field carrying `@skip` or `@include` is legitimately absent from a
spec-correct response whenever its condition excludes it, independent of the
field's schema nullability. octoqlgen therefore generates such a field as a
pointer, even when the schema type is non-null, so that absence is representable
as `nil`:

```graphql
query MyQuery($hide: Boolean!) {
  user {
    login
    isSuspended @skip(if: $hide)
  }
}
```

generates:

```go
type MyQueryUser struct {
	Login       string
	IsSuspended *bool
}
```

`IsSuspended` is a pointer even though `isSuspended: Boolean!` is non-null in the
schema. When the server omits the field, it decodes to `nil` rather than to
`false`. Without the pointer the omitted field would silently decode to the Go
zero value, and a check shaped like `if user.IsSuspended { deny() }` would fail
**open** — treating a suspended-but-omitted user as not suspended. Read these
fields with a nil guard:

```go
if user.IsSuspended != nil && *user.IsSuspended {
	deny()
}
```

A list under `@skip` keeps its nil-ability at the container level rather than
wrapping its elements: a slice is already nil-able in Go, so `[Role!]!` stays
`[]Role` (never `[]*Role`), and an omitted list decodes to a nil slice. Fields
whose Go type can already hold nil — nullable schema types, slices, and generated
abstract interfaces — keep their existing types. The forced pointer therefore
applies only to scalars, enums, and structs, whose Go zero value would otherwise
be indistinguishable from an absent value.

Because absence must stay representable, combining `@skip` or `@include` with
[`pointer: false`](#pointer) on the same field is a contradiction and is rejected
as a generation error.

`@skip` and `@include` are only supported on fields. Applied to a fragment
spread or an inline fragment they make every field the fragment contributes
absent at once, and those fields are commonly flattened into the parent struct,
so octoqlgen cannot represent their absence and rejects the operation with a
generation error. Apply the directive to the individual fields instead:

```graphql
# rejected
query MyQuery($hide: Boolean!) {
  user {
    ...UserFields @skip(if: $hide)
  }
}

# supported: move @skip onto the fields
query MyQuery($hide: Boolean!) {
  user {
    login @skip(if: $hide)
    isSuspended @skip(if: $hide)
  }
}
```
