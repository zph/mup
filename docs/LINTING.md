# Linting and Error Checking

This project uses multiple tools to ensure code quality and catch ignored errors.

## Quick Start

```bash
# Run all checks
make check

# Run full linter (includes errcheck)
make lint
```

## Tools Used

### 1. golangci-lint

A fast Go linter that runs multiple linters in parallel. Configuration is in `.golangci.yml`.

**Key linters enabled:**
- **errcheck**: Checks that all errors are handled
- **errorlint**: Suggests proper error wrapping
- **errname**: Ensures error variables follow naming conventions
- **govet**: Go's built-in vet tool (runs automatically)
- **staticcheck**: Static analysis including error checking
- Plus many more code quality checks

**Installation:**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

**Usage:**
```bash
make lint
# or
golangci-lint run ./...
```

### 2. errcheck (via golangci-lint)

The `errcheck` linter is included in golangci-lint and configured in `.golangci.yml`. It checks that all errors are handled and flags ignored errors.

**Configuration:**
- Enabled in `.golangci.yml` with `check-type-assertions: true` and `check-blank: true`
- Ignores common safe-to-ignore functions (fmt.Print*, os.Remove*, etc.)

**Usage:**
```bash
make lint
# errcheck runs as part of golangci-lint
```

### 3. go vet (via golangci-lint)

The `govet` linter is included in golangci-lint and runs automatically as part of `make lint`. No separate command needed.

## Common Error Patterns to Avoid

### ❌ Bad: Ignoring Errors

```go
// BAD - error is ignored
result, err := someFunction()
// err is never checked!

// BAD - explicitly ignoring error
_, err := someFunction()
// Still ignoring the error
```

### ✅ Good: Handling Errors

```go
// GOOD - check and handle error
result, err := someFunction()
if err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}

// GOOD - if error is truly safe to ignore, document why
result, err := someFunction()
if err != nil {
    // This error is safe to ignore because...
    log.Debugf("non-critical operation failed: %v", err)
}
```

### ✅ Good: Using errcheck Ignore Comments

For cases where you truly need to ignore an error (rare!), use an errcheck ignore comment:

```go
//nolint:errcheck // This error is safe to ignore because...
result, err := someFunction()
```

Or use errcheck's ignore file (`.errcheck-ignore`) for specific functions:

```
# .errcheck-ignore
fmt\.Print.*
os\.Remove.*
```

## CI/CD Integration

The GitHub Actions workflow (`.github/workflows/lint.yml`) automatically runs:
1. `golangci-lint` (includes go vet, errcheck, and other linters)

All checks must pass for PRs to be merged.

## Pre-commit Hooks

To catch errors before committing, install pre-commit hooks:

```bash
# Install pre-commit
pip install pre-commit

# Install hooks
pre-commit install

# Copy example config
cp .pre-commit-config.yaml.example .pre-commit-config.yaml
```

Now errors will be checked automatically before each commit.

## Configuration

### golangci-lint

Configuration is in `.golangci.yml`. Key settings:

- **errcheck**: Checks all errors, ignores common safe-to-ignore functions (fmt.Print*, os.Remove*, etc.)
- **errorlint**: Checks for proper error wrapping with `%w` verb
- **errname**: Ensures error variables are named `err` or `Err*`

### errcheck

The `errcheck` linter is configured in `.golangci.yml` under `linters-settings.errcheck`. To customize ignored functions, update the `ignore` regex pattern in the config file.

## Troubleshooting

### "Too many false positives"

If a linter reports an error that you believe is safe to ignore:

1. **First**: Consider if the error should actually be handled
2. **If truly safe**: Add a `//nolint` comment:
   ```go
   //nolint:errcheck // Safe to ignore: this is a cleanup operation
   os.Remove(tempFile)
   ```

3. **For errcheck**: Add to `.errcheck-ignore` file

### "Linter is too slow"

golangci-lint can be slow on large codebases. Options:

1. Run only specific linters:
   ```bash
   golangci-lint run --enable=errcheck,errorlint ./...
   ```

2. Use `--fast` mode (fewer linters):
   ```bash
   golangci-lint run --fast ./...
   ```

3. Exclude slow linters in `.golangci.yml`

## Best Practices

1. **Always check errors** - Even if you think they can't happen
2. **Wrap errors** - Use `fmt.Errorf("context: %w", err)` for better error messages
3. **Don't ignore errors silently** - At minimum, log them
4. **Run checks before committing** - Use pre-commit hooks or `make check`
5. **Fix linting issues in CI** - Don't disable checks, fix the code

## Resources

- [golangci-lint Documentation](https://golangci-lint.run/)
- [errcheck Documentation](https://github.com/kisielk/errcheck)
- [Go Error Handling Best Practices](https://go.dev/blog/error-handling-and-go)
