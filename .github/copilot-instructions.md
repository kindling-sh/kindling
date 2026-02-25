# Copilot Instructions

## Active Feature Branches

### Provider Abstraction (`provider-abstraction` branch)

All work on the CI provider abstraction feature **must** be done on the
`provider-abstraction` branch. This includes:

- Changes to `pkg/ci/` (interfaces, registry, provider implementations)
- Refactoring operator controller or CLI code to use `ci.Default()`
- Adding new provider implementations (e.g. GitLab CI)
- Updating docs related to the provider abstraction

Do **not** merge provider abstraction changes into `main` or `fuzz`
until the feature is complete and tested.
