# Contributing to NovaAI Gateway

Thank you for your interest in contributing to NovaAI Gateway!

## Code of Conduct

Please be respectful and professional when contributing. We aim to foster an inclusive and welcoming community.

## How to Contribute

### Reporting Bugs

1. Check if the bug has already been reported
2. Create a detailed issue with:
   - Clear title and description
   - Steps to reproduce
   - Expected vs actual behavior
   - Go version and OS
   - Any relevant logs

### Suggesting Features

1. Open an issue with `[Feature Request]` prefix
2. Describe the feature and its use case
3. Explain why this feature would be beneficial
4. Provide any mockups or examples if applicable

### Pull Requests

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Make your changes with proper testing
4. Ensure code follows project conventions
5. Write clear commit messages
6. Submit a pull request

## Development Setup

```bash
# Clone the repository
git clone https://github.com/yourusername/novaairouter.git
cd novaairouter

# Install dependencies
go mod download

# Run tests
go test ./...

# Build
go build -o novaairouter ./cmd/gateway
```

## Code Style

- Follow standard Go conventions
- Use meaningful variable and function names
- Add comments for exported functions
- Keep functions focused and concise
- Write unit tests for new features

## Testing

- Unit tests go in `tests/unit/`
- Integration tests go in `tests/integration/`
- Test file naming: `test_*.go` or `*_test.go`
- Run tests before submitting PR

```bash
# Run all tests
go test ./...

# Run specific test
go test -v ./tests/unit/...
```

## Commit Messages

- Use clear, descriptive commit messages
- Start with a verb (Add, Fix, Update, Remove)
- Reference issues when applicable
- Example: `Add load balancing algorithm selection`

## Review Process

1. Maintainers will review your PR
2. Address any feedback promptly
3. Once approved, your PR will be merged

## Documentation

- Update README.md for user-facing changes
- Add code comments for complex logic
- Update docs/ folder for API changes

## Questions?

Feel free to open an issue for questions about contributing.
