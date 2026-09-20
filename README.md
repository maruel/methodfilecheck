# methodfilecheck

methodfilecheck reports Go methods whose receiver type is declared in another
source file of the same package, and method blocks interleaved with other
declarations. Constructors — free functions whose first result is `T` or `*T` —
may sit between type `T` and its methods.

Every violation comes with a mechanical fix, so the code can be reordered
automatically: moved blocks keep their doc comments, edited files are rewritten
gofmt clean, and goimports runs on them when installed.

## Command line

```bash
# Check the module in the current directory.
go install github.com/maruel/methodfilecheck/cmd/methodfilecheck@latest
methodfilecheck ./...

# Reorder the code in place to resolve the violations.
methodfilecheck -fix ./...
```

## golangci-lint

The package ships a [golangci-lint module
plugin](https://golangci-lint.run/docs/plugins/module-plugins/). Build the
custom linter binary and run it instead of `golangci-lint`:

```bash
# .custom-gcl.yml, next to .golangci.yml
version: "2"
plugins:
  - module: github.com/maruel/methodfilecheck
    import: github.com/maruel/methodfilecheck
    path: ../methodfilecheck # or a published version: version: v1.x.y
```

```yaml
# .golangci.yml
version: "2"
linters:
  enable:
    - methodfilecheck
  settings:
    custom:
      methodfilecheck:
        type: module
        description: Methods must be declared in the same file as their receiver type, in one contiguous block.
        settings: {}
```

```bash
golangci-lint custom --version v2.13.2
./custom-gcl run ./...
```

The plugin carries suggested fixes for same-file violations, so
`golangci-lint run --fix` reorders the code automatically. Cross-file moves are
report-only there; apply them with `methodfilecheck -fix`.

## What it checks

- A method must live in the same file as its receiver type, directly after the
  type declaration and its constructors.
- A constructor of a type comes before the type's first method, so the type,
  its constructors, and its methods stay together.
- All methods of one receiver must form one contiguous block; free helpers and
  constructors may sit inside it, another receiver's methods may not.
- Files with `//go:build` constraints are only checked against themselves, and
  generated files are skipped. XTest files form their own package group, since
  methods cannot move between them.

## License

Apache 2.0; see the LICENSE file.
