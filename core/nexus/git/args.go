package git

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrInvalidArgument is wrapped by every refusal of a guest-supplied value, so callers and tests can tell
// "refused by validation" from "git ran and failed".
var ErrInvalidArgument = errors.New("invalid argument")

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
		return fmt.Errorf("%w: remote name %q: only a configured remote's name is accepted", ErrInvalidArgument, clip(name))
	}
	return nil
}

func validateRef(ref string) error {
	bad := fmt.Errorf("%w: ref %q", ErrInvalidArgument, clip(ref))
	if len(ref) == 0 || len(ref) > maxArgLen || !refRe.MatchString(ref) || ref == "@" ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, ".") {
		return bad
	}
	// The git check-ref-format rules per path component: not empty, not starting with a dot, not ending in .lock.
	for _, comp := range strings.Split(ref, "/") {
		if comp == "" || strings.HasPrefix(comp, ".") || strings.HasSuffix(comp, ".lock") {
			return bad
		}
	}
	return nil
}

// validateBranchRef is validateRef for a name the operation will CREATE or MOVE (a clone branch, a push
// destination): a short branch or tag name, or a fully qualified refs/heads/ or refs/tags/ name. HEAD and
// other refs/ namespaces (refs/remotes, refs/notes, refs/replace, refs/stash, ...) are not accepted.
func validateBranchRef(ref string) error {
	if err := validateRef(ref); err != nil {
		return err
	}
	bad := fmt.Errorf("%w: ref %q is not a branch or tag name", ErrInvalidArgument, clip(ref))
	if ref == "HEAD" || strings.EqualFold(ref, "FETCH_HEAD") || strings.EqualFold(ref, "ORIG_HEAD") {
		return bad
	}
	if strings.HasPrefix(ref, "refs/") && !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
		return bad
	}
	return nil
}

// validateRefspec accepts "<ref>" or "<ref>:<ref>". A forced ("+") or deleting (empty source) refspec is
// refused: the typed ABI never offered either, and both change remote history.
func validateRefspec(spec string) error {
	src, dst, hasDst := strings.Cut(spec, ":")
	if err := validateRef(src); err != nil {
		return fmt.Errorf("%w: refspec %q", ErrInvalidArgument, clip(spec))
	}
	if hasDst {
		if err := validateBranchRef(dst); err != nil {
			return fmt.Errorf("%w: refspec %q", ErrInvalidArgument, clip(spec))
		}
	}
	return nil
}

// validateCloneURL accepts https:// and ssh:// URLs with a plain host and no password. Everything else
// is refused: scp-like "host:path", transport helpers such as "ext::", and, unless the operator enabled
// local sources, local paths and file://. A value that does not parse is refused, never waved through.
//
// With local sources enabled (tests and operator-owned wiring only; the guest cannot set it) the only
// extra form accepted is an absolute, already-clean filesystem path or a file:// URL with no host and
// such a path. Such a value is recognised explicitly; it is not "whatever failed to parse as a URL".
//
// Error messages never contain the URL: a URL can carry credentials.
func validateCloneURL(raw string, allowLocal bool) error {
	bad := func(reason string) error { return fmt.Errorf("%w: clone url: %s", ErrInvalidArgument, reason) }
	if len(raw) == 0 || len(raw) > maxArgLen || strings.HasPrefix(raw, "-") {
		return bad("empty, too long or leading dash")
	}
	// Exactly one clean URL, in the printable ASCII range: no control characters (a newline would end a
	// record), no whitespace of any kind, no non-ASCII character (Unicode spaces, zero-width and
	// direction-control characters can make two different strings look alike). An internationalised host
	// is given in its ASCII (punycode) form.
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] >= 0x7f {
			return bad("only printable ASCII without whitespace is accepted")
		}
	}
	if allowLocal && isLocalSource(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return bad("not a valid URL")
	}
	if u.Scheme != "https" && u.Scheme != "ssh" {
		return bad("only https:// and ssh:// are accepted")
	}
	if u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") || strings.ContainsAny(u.Host, " \t\\`$;|&<>") {
		return bad("missing or unsafe host")
	}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			return bad("credentials in the URL are not accepted")
		}
	}
	return nil
}

// isLocalSource reports whether raw is an absolute clean path or a file:// URL naming one.
func isLocalSource(raw string) bool {
	if rest, ok := strings.CutPrefix(raw, "file://"); ok {
		u, err := url.Parse(raw)
		return err == nil && u.Host == "" && rest != "" && filepath.IsAbs(u.Path) && filepath.Clean(u.Path) == u.Path
	}
	return !strings.Contains(raw, ":") && filepath.IsAbs(raw) && filepath.Clean(raw) == raw
}

// credentialsInURL matches "scheme://userinfo@" so userinfo can be masked wherever text may carry a URL.
var credentialsInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s@]*@`)

// scrubURLs masks URL userinfo in s. It is applied to every message that can leave the package: error
// text, git's own output, and log attributes.
func scrubURLs(s string) string { return credentialsInURL.ReplaceAllString(s, "${1}***@") }

// validatePath checks one pathspec the guest wants staged. It must be a plain relative path inside the
// repository; pathspec magic, absolute and parent-escaping paths and the .git directory are refused.
func validatePath(p string) error {
	if len(p) == 0 || len(p) > maxArgLen || hasControl(p) || strings.HasPrefix(p, "-") ||
		strings.HasPrefix(p, ":") || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("%w: path %q", ErrInvalidArgument, clip(p))
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("%w: path %q", ErrInvalidArgument, clip(p))
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
