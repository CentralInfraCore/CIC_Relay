#!/usr/bin/env python3
"""Count the mutants a listing command prints, failing closed.

Usage: count_mutants.py -- cargo mutants --workspace --list [--in-diff FILE]

The exit status of the listing command is checked BEFORE its output is counted:

* the command fails (any non-zero status, or it cannot be started)  -> exit 1, nothing on
  stdout. A failed listing is never "zero mutants".
* the command succeeds and prints nothing                          -> prints 0 (a valid,
  empty listing: the diff applies and holds no mutable code).
* the command succeeds and prints N non-empty lines                -> prints N.

stdout carries only the number, so `n=$(count_mutants.py -- ...)` is safe under `bash -e`:
a failed listing fails the assignment and with it the step. The listing itself goes to
--out (default: a temporary file that is removed) so that it can be kept as evidence.
"""

import argparse
import os
import subprocess
import sys
import tempfile


def count_listing(cmd: list[str], out_path: str) -> int:
    """Run cmd with stdout redirected to out_path; return the number of non-empty lines.

    Raises RuntimeError when the command cannot be started or exits non-zero.
    """
    try:
        with open(out_path, "wb") as out:
            proc = subprocess.run(cmd, stdout=out, check=False)
    except OSError as e:
        raise RuntimeError(f"cannot run the listing command {cmd!r}: {e}") from e
    if proc.returncode != 0:
        raise RuntimeError(
            f"the listing command failed with exit status {proc.returncode}: {' '.join(cmd)}"
        )
    with open(out_path, "rb") as fh:
        return sum(1 for line in fh if line.strip())


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--out", help="keep the raw listing here (default: temporary)")
    ap.add_argument("cmd", nargs=argparse.REMAINDER, help="-- <listing command...>")
    a = ap.parse_args(argv)
    cmd = a.cmd[1:] if a.cmd[:1] == ["--"] else a.cmd
    if not cmd:
        ap.error("a listing command is required after --")

    if a.out:
        path, tmp = a.out, False
    else:
        fd, path = tempfile.mkstemp(prefix="mutants-listing-")
        os.close(fd)
        tmp = True
    try:
        n = count_listing(cmd, path)
    except RuntimeError as e:
        print(f"::error::{e}", file=sys.stderr)
        return 1
    finally:
        if tmp:
            os.unlink(path)
    print(n)
    return 0


if __name__ == "__main__":
    sys.exit(main())
