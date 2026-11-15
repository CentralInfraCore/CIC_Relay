package git

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Everything the WASM guest hands to a git operation is untrusted. The operations below pass these
// values to the git binary as arguments, so a value that begins with "-" would be read as an option,
// and some options (a replacement pack program, a configuration override) make git run a program of the
// caller's choosing. The validators here are the single choke point: each operation validates every
// guest-supplied value before it builds a command line, and the command lines also separate options
// from operands where git allows it. Both layers are intended; neither is the only defence.

const maxArgLen = 512

var (
	// remoteNameRe: a configured remote's name, never a URL or path.
	remoteNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// refRe: a branch, tag or ref name. No leading dash, no whitespace, no revision syntax.
	refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+@-]*$`)
)

func validateRemoteName(name string) error {
	if len(name) == 0 || len(name) > maxArgLen || !remoteNameRe.MatchString(name) {
		return fmt.Errorf("invalid remote name %q: only a configured remote's name is accepted", clip(name))
	}
	return nil
}

func validateRef(ref string) error {
	if len(ref) == 0 || len(ref) > maxArgLen || !refRe.MatchString(ref) ||
		strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("invalid ref %q", clip(ref))
	}
	return nil
}

// validateRefspec accepts "<ref>" or "<ref>:<ref>". A forced ("+") or deleting (empty source) refspec is
// refused: the typed ABI never offered either, and both change remote history.
func validateRefspec(spec string) error {
	src, dst, hasDst := strings.Cut(spec, ":")
	if err := validateRef(src); err != nil {
		return fmt.Errorf("invalid refspec %q", clip(spec))
	}
	if hasDst {
		if err := validateRef(dst); err != nil {
			return fmt.Errorf("invalid refspec %q", clip(spec))
		}
	}
	return nil
}

// validateCloneURL accepts https:// and ssh:// URLs with a plain host. Local paths, file://, scp-like
// "host:path", and transport helpers such as "ext::" are refused unless the operator enabled local
// sources (tests and operator-owned wiring only; the guest cannot set it).
func validateCloneURL(raw string, allowLocal bool) error {
	if len(raw) == 0 || len(raw) > maxArgLen || strings.HasPrefix(raw, "-") || hasControl(raw) {
		return fmt.Errorf("invalid clone url %q", clip(raw))
	}
	if allowLocal && !strings.Contains(raw, "::") && !strings.HasPrefix(raw, "-") {
		if u, err := url.Parse(raw); err != nil || u.Scheme == "" || u.Scheme == "file" {
			return nil // a local path or file:// source, operator-enabled
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Hostname() == "" ||
		strings.HasPrefix(u.Hostname(), "-") || strings.ContainsAny(u.Host, " \t\\`$;|&<>") {
		return fmt.Errorf("invalid clone url %q: only https:// and ssh:// are accepted", clip(raw))
	}
	return nil
}

// validatePath checks one pathspec the guest wants staged. It must be a plain relative path inside the
// repository; pathspec magic, absolute and parent-escaping paths and the .git directory are refused.
func validatePath(p string) error {
	if len(p) == 0 || len(p) > maxArgLen || hasControl(p) || strings.HasPrefix(p, "-") ||
		strings.HasPrefix(p, ":") || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("invalid path %q", clip(p))
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("invalid path %q", clip(p))
		}
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func clip(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}
