// Package lint holds repository-wide checks that run as tests (go test
// ./internal/lint): plugin import boundaries and user-visible string
// literals that bypass i18n. See docs/design.md §5.1 and §10.5.
package lint
