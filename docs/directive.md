# octoqlgen's directives

octoqlgen is configured from your operations by three directives, one for each
scope an option can have:

| Directive | Scope | Goes on |
| --- | --- | --- |
| `@octoqlgen` | the node it is attached to | fields, variables, operations, fragments |
| [`@octoqlgenDefaults`](#octoqlgendefaults) | the fields inside | operations, fragments |
| [`@octoqlgenFor`](#octoqlgenfor) | a named type's field, everywhere | operations, fragments |

They are real GraphQL directives, written directly on the element they apply to.
octoqlgen declares them in the schema it loads and removes them from every
operation before sending it, so the server neither has to define them nor ever
sees them.

Having one directive per scope means each declares only the options its scope
supports, so an option written where it would do nothing is a GraphQL error your
editor can show, rather than something octoqlgen has to check.

These are the only supported spellings. There are no compatibility aliases.

Earlier versions of octoqlgen took these options in a comment on the preceding
line. That form is no longer accepted, and octoqlgen reports an error rather
than generating with its options silently dropped. Move each option onto the
element it applies to:

```graphql
# no longer supported
# @octoqlgen(pointer: false)
isSuspended

# write this instead
isSuspended @octoqlgen(pointer: false)
```

## Editor support

`@octoqlgen` has to be declared in a schema for editors and other GraphQL
tooling to resolve it. octoqlgen declares it internally, so generation works
without any declaration on disk, but an editor reading only the schema file
reports every use of the directive as unknown.

To fix that, octoqlgen writes `octoqlgen-directive.graphql` next to any schema
it materializes, holding just the declaration. The schema itself keeps the exact
bytes its source served, so `schema.sha256` still describes it.

Tooling that reads every `.graphql` file in the project picks the declaration up
with no further setup. Tooling that takes an explicit list, such as
[graphql-config](https://the-guild.dev/graphql/config), needs it added:

```yaml
schema:
  - schema.graphql
  - octoqlgen-directive.graphql
```

Declare it in exactly one place per project. GraphQL does not allow a directive
to be declared twice, so tooling that reads several schema files at once reports
an error if more than one of them declares `@octoqlgen`.

## Placement

`@octoqlgen` applies only to the element it is attached to, not to that
element's arguments or selections. To reach the elements inside an operation or
fragment, use `@octoqlgenDefaults`; to reach a named type's field, use
`@octoqlgenFor`.

String option values must not be empty. Use `bind: "-"` to explicitly opt out
of a configured binding.

For example:

```graphql
# Comment describing the query
query MyQuery(
  $arg1: String
  $arg2: String @octoqlgen(n: "d")
  $arg3: MyInput
) @octoqlgenDefaults(n: "b") @octoqlgenDefaults(n: "c") {
  field1 @octoqlgen(n: "e")
  field2
  field3(argument: "value") @octoqlgen(n: "f") {
    field4
    field5
  }
}
```

Here `b` and `c` apply to all relevant nodes in the query, `d` applies to
`$arg2`, `e` applies to `field1`, and `f` applies to `field3` but not to its
argument, `field4`, or `field5`.

An `@octoqlgen` on a node takes precedence over a default, so `d`, `e`, and `f`
take precedence over `b` and `c`.

`@octoqlgen` does *not* apply to a node's children, so `d` does not apply to the
fields of `MyInput` and `f` does not apply to `field4`. Defaults do apply to
children, so both `b` and `c` apply to the fields of `MyInput` and to `field4`.

Each directive may be repeated in the same location as long as the repeats do
not have conflicting options. Placing one anywhere other than the locations in
the table above is a GraphQL validation error. octoql does not support
subscriptions.

## `@octoqlgenDefaults`

Sets `omitempty`, `pointer`, or `struct` as a default for the fields inside an
operation or fragment. A directive on a node takes precedence.

```graphql
query MyQuery @octoqlgenDefaults(pointer: false) {
  myField
  myOtherField @octoqlgen(pointer: true)
}
```

`@octoqlgen` describes the node it is attached to, so these options are rejected
there on an operation or fragment; write them as defaults instead. The remaining
options are not defaults at all: `typename` and `alias` name a single generated
construct, so applying them to every field would ask for one name to be used
many times, and `flatten` is only meaningful where a selection is a single
fragment spread.

## `@octoqlgenFor`

Applies options to the named field of the named type, written as
`"MyType.myField"`, wherever octoqlgen generates that field. It goes on an
operation or fragment.

This is how to reach an input type's fields. They are defined by the schema
rather than written in your operation, so there is no place to attach a
directive to them.

```graphql
query MyQuery($arg: MyInput)
  @octoqlgenFor(field: "MyInput.myField", omitempty: true)
  @octoqlgenFor(field: "MyInput.myOtherField", pointer: true)
  @octoqlgenFor(field: "MyOutput.id", bind: "path/to/pkg.MyOutputID")
{ ... }
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

A named type generates one Go type, so declarations for the same field have to
agree. Two operations that ask for different things cannot both be satisfied;
octoqlgen rejects that rather than letting whichever operation it converts first
decide, which would make the generated code depend on the order of your
operations.

Declarations only have to agree with each other. An operation that says nothing
about a field is not disagreeing, so adding an operation that happens to use the
type does not oblige it to repeat the declaration.

To give two operations genuinely different Go types for one GraphQL type, give
them different names with [`typename`](#typename), or annotate the selected
fields directly instead.

`struct` and `flatten` are not available here: both depend on the shape of a
particular selection, which a type-wide declaration does not have.

## `omitempty`

Omits this argument, or input-type field when combined with
[`@octoqlgenFor`](#octoqlgenfor), if it has an empty value. Empty is defined the
same as in `encoding/json`: false, 0, a nil pointer, a nil interface value, and
any empty array, slice, map, or string.

Given the following query:

```graphql
query MyQuery($arg: String @octoqlgen(omitempty: true)) { ... }
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
  myField @octoqlgen(flatten: true) {
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
  myField @octoqlgen(alias: "MyGreatName")
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

Only applicable to selected fields, either directly or through
[`@octoqlgenFor`](#octoqlgenfor). A variables-struct field is named after its
variable and an input-type field after its GraphQL field, so `alias` is rejected
in both places rather than accepted and ignored. Rename the variable itself to
change the Go name of a variables field.

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
myField @octoqlgen(bind: "[]time.Time")
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
query MyQuery @octoqlgen(typename: "MyResp") {
  user @octoqlgen(typename: "User") {
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
    name @octoqlgen(typename: "NameType")
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
whose Go type can already hold nil — nullable schema types, slices, generated
abstract interfaces, and bound Go types that are themselves nil-able (a pointer,
slice, map, or `interface{}`) — keep their existing types. The forced pointer
therefore applies only to types whose Go zero value would otherwise be
indistinguishable from an absent value: scalars, enums, structs, and bound
fixed-size arrays.

For a [`bind:`](#bind) type, nil-ability is judged **syntactically** from how the
binding is spelled — octoqlgen does not resolve the underlying type of a named
binding. Only the recognized literal forms are treated as already nil-able and
left unwrapped: `*T`, `[]T`, `map[...]T`, and `interface{}`. A named type or
alias whose underlying type is nil-able — for example `example.com/pkg.Tags`
where `type Tags []string` — is **conservatively wrapped** (`*Tags`), because
octoqlgen cannot see through the name to know it is already a slice. This is the
safe direction: an unnecessary pointer, never a missing one. The bare
predeclared identifier `any` is normalized to `interface{}` (which cannot be
shadowed) and left unwrapped; bind to a package-qualified name if you have
deliberately shadowed `any` with your own non-nil-able type.

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

One exception applies to the individual-field form above: when a field's
selection is bound to a caller-supplied Go type — through a local
[`bind:`](#bind) or a global binding — octoqlgen treats that selection as opaque
and never generates its nested fields, so it cannot make any of them nil-able. A
`@skip` or `@include` on a field *nested inside* such a bound composite is
therefore also rejected with a generation error, even though moving the directive
onto individual fields is otherwise the supported fix. A directive on the bound
field itself is unaffected; only selections beneath an opaque binding are
rejected.

One field is excluded from the forced-pointer support above: `__typename` within
an interface or union selection must not carry `@skip` or `@include`. octoqlgen
relies on `__typename` to decode the concrete type of an abstract value, so a
conditionally-omitted `__typename` leaves the response undecodable and fails at
runtime. Request `__typename` unconditionally on abstract selections.
