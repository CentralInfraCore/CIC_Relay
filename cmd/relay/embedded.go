package main

import "embed"

// embeddedFS contains all static assets bundled at compile time.
// Subdirectories:
//   - embedded/pki      — CA certificates (dev trust anchor)
//   - embedded/schemas  — built-in schema YAML files
//   - embedded/workflows — built-in workflow YAML files

//go:embed embedded
var embeddedFS embed.FS
