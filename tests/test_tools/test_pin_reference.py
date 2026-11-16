"""The reference of the measuring-tool check is not the pull request's own (CIC_Relay #195).

Every case runs real git against a bare "origin" and a clone: the base branch tip is the reference, a forged SHA is
refused because it is not a commit of the base branch, a changed pin file needs an approval bound to the exact head and
the exact digest and written by an approver of the BASE pin file. Each denial has a positive control next to it.
"""

import json
import os
import subprocess
import sys

import pytest

SCRIPTS = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../scripts"))
sys.path.insert(0, SCRIPTS)

import quality_gate as qg  # noqa: E402

GATE = os.path.join(SCRIPTS, "quality_gate.py")
PINS = ".measurement-tools.json"


def git(repo, *args):
    env = dict(os.environ, GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null")
    r = subprocess.run(
        ["git", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        env=env,
        check=True,
    )
    return r.stdout.strip()


def pins_doc(go="go1.26.11", approvers=("owner",), surfaces=None):
    return {
        "schema": "measurement-tools/1",
        "approvers": list(approvers),
        "tools": {"go": go, "rustc": "1.96.1"},
        "surfaces": surfaces or {"coverage-go": ["go"], "mutation-rust": ["rustc"]},
    }


def write_pins(repo, doc):
    with open(os.path.join(repo, PINS), "w") as fh:
        json.dump(doc, fh, indent=2, sort_keys=True)
        fh.write("\n")


def commit(repo, msg):
    git(repo, "add", "-A")
    git(
        repo,
        "-c",
        "user.name=t",
        "-c",
        "user.email=t@example.com",
        "commit",
        "-q",
        "--no-gpg-sign",
        "-m",
        msg,
    )
    return git(repo, "rev-parse", "HEAD")


@pytest.fixture
def world(tmp_path):
    """origin (bare) with main holding the pins and a script that already has the mechanism; a clone of it."""
    origin = tmp_path / "origin.git"
    subprocess.run(
        ["git", "init", "-q", "--bare", "-b", "main", str(origin)], check=True
    )
    work = tmp_path / "work"
    subprocess.run(["git", "clone", "-q", str(origin), str(work)], check=True)
    git(work, "checkout", "-q", "-b", "main")
    os.makedirs(work / "scripts")
    (work / "scripts" / "quality_gate.py").write_text(
        "# has the pin-reference subcommand\n"
    )
    write_pins(work, pins_doc())
    base = commit(work, "base")
    git(work, "push", "-q", "origin", "main")
    return {"work": str(work), "base": base, "tmp": tmp_path}


def branch(w, name="pr"):
    git(w["work"], "checkout", "-q", "-b", name, w["base"])


def run_ref(w, head, comments=None, base_sha=None, mode="pull_request", **kw):
    cpath = ""
    if comments is not None:
        cpath = str(w["tmp"] / "comments.json")
        with open(cpath, "w") as fh:
            json.dump(comments, fh)
    return qg.pin_reference(
        mode=mode,
        pins=PINS,
        out_dir=str(w["tmp"] / "out"),
        repo=w["work"],
        base_ref="main",
        base_sha=base_sha or w["base"],
        head_sha=head,
        comments=cpath,
        **kw,
    )


def approval(head, digest, login="owner", cid=7):
    return {
        "id": cid,
        "login": login,
        "body": f"looks right\n{qg.APPROVAL_PREFIX} head={head} digest={digest}\n",
    }


def head_digest(w, head):
    return qg._digest(
        subprocess.run(
            ["git", "-C", w["work"], "show", f"{head}:{PINS}"],
            capture_output=True,
            check=True,
        ).stdout
    )


def test_unchanged_pin_uses_the_base_reference(world):
    branch(world)
    (world["tmp"] / "work" / "x.txt").write_text(
        "a change that does not touch the pins"
    )
    head = commit(world["work"], "feature")
    rec = run_ref(world, head)
    assert rec["status"] == "UNCHANGED"
    assert rec["reference_commit"] == world["base"]
    assert rec["reference_digest"] == rec["head_digest"]


def test_a_pin_change_without_approval_is_refused(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "bump together with the setup")
    with pytest.raises(qg.GateError) as e:
        run_ref(world, head, comments=[])
    msg = str(e.value)
    assert "[PIN_CHANGED_UNAPPROVED]" in msg
    assert f"head={head}" in msg and head_digest(world, head) in msg


def test_a_removed_tool_or_a_narrowed_surface_is_refused(world):
    branch(world)
    doc = pins_doc()
    del doc["tools"]["rustc"]
    doc["surfaces"] = {"coverage-go": ["go"]}
    write_pins(world["work"], doc)
    head = commit(world["work"], "drop the mandatory tool")
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[])


def test_an_approval_of_this_head_and_digest_makes_the_head_pins_the_reference(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "toolchain update")
    d = head_digest(world, head)
    rec = run_ref(world, head, comments=[approval(head, d)])
    assert rec["status"] == "APPROVED_UPDATE"
    assert rec["approved_by"] == "owner" and rec["approval_id"] == 7
    assert rec["reference_digest"] == d and rec["base_digest"] != d
    ref = json.load(open(rec["pins_file"]))
    assert ref["tools"]["go"] == "go1.99.0"


def test_an_approval_does_not_survive_a_new_push(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    old = commit(world["work"], "toolchain update")
    ok = approval(old, head_digest(world, old))
    assert run_ref(world, old, comments=[ok])["status"] == "APPROVED_UPDATE"
    (world["tmp"] / "work" / "y.txt").write_text("one more push after the approval")
    new = commit(world["work"], "push after the approval")
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, new, comments=[ok])


def test_an_approval_of_another_digest_is_refused(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "toolchain update")
    wrong = "sha256:" + "0" * 64
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[approval(head, wrong)])


def test_only_an_approver_of_the_base_can_approve(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "toolchain update")
    d = head_digest(world, head)
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[approval(head, d, login="someone-else")])


def test_the_pr_cannot_add_itself_to_the_approvers(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0", approvers=("owner", "mallory")))
    head = commit(world["work"], "update and self-approve")
    d = head_digest(world, head)
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[approval(head, d, login="mallory")])


def test_a_base_without_approvers_cannot_approve_anything(world):
    git(world["work"], "checkout", "-q", "main")
    write_pins(world["work"], pins_doc(approvers=()))
    base = commit(world["work"], "no approvers")
    git(world["work"], "push", "-q", "origin", "main")
    world["base"] = base
    git(world["work"], "checkout", "-q", "-b", "pr", base)
    write_pins(world["work"], pins_doc(go="go1.99.0", approvers=()))
    head = commit(world["work"], "update")
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[approval(head, head_digest(world, head))])


def test_a_forged_reference_sha_is_refused(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    forged = commit(world["work"], "a commit of the PR itself, offered as the base")
    (world["tmp"] / "work" / "z.txt").write_text("more")
    head = commit(world["work"], "head")
    with pytest.raises(qg.GateError, match="REFERENCE_FORGED"):
        run_ref(world, head, base_sha=forged)
    # positive control: the real base SHA is accepted (the pins differ, so it is the approval that is missing)
    with pytest.raises(qg.GateError, match="PIN_CHANGED_UNAPPROVED"):
        run_ref(world, head, comments=[])


def test_a_deleted_pin_file_is_refused_even_with_an_approval(world):
    branch(world)
    os.remove(os.path.join(world["work"], PINS))
    head = commit(world["work"], "delete the pins")
    with pytest.raises(qg.GateError, match="PIN_DELETED"):
        run_ref(world, head, comments=[approval(head, "sha256:" + "0" * 64)])


def test_an_unreadable_comments_source_is_fail_closed(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "update")
    with pytest.raises(qg.GateError):
        qg.pin_reference(
            mode="pull_request",
            pins=PINS,
            out_dir=str(world["tmp"] / "out"),
            repo=world["work"],
            base_ref="main",
            base_sha=world["base"],
            head_sha=head,
            comments=str(world["tmp"] / "missing.json"),
        )


def test_push_events_use_the_tree_itself_and_say_so(world):
    rec = run_ref(world, world["base"], mode="push")
    assert rec["status"] == "SELF_REFERENCE"
    assert rec["reference_commit"] == world["base"]


def test_a_pull_request_without_a_full_base_is_invalid(world):
    branch(world)
    head = world["base"]
    with pytest.raises(qg.GateError, match="REFERENCE_INVALID"):
        qg.pin_reference(
            mode="pull_request",
            pins=PINS,
            out_dir=str(world["tmp"] / "out"),
            repo=world["work"],
            base_ref="",
            base_sha="abc",
            head_sha=head,
        )


def test_bootstrap_is_only_for_the_change_that_adds_the_mechanism(world):
    branch(world)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head = commit(world["work"], "adds the mechanism")
    # the base already has it (its script mentions pin-reference): the bootstrap is refused
    with pytest.raises(qg.GateError, match="BOOTSTRAP_REFUSED"):
        run_ref(world, head, mode="bootstrap")
    # a base without the mechanism: the bootstrap is allowed once, and says so
    git(world["work"], "checkout", "-q", "main")
    (world["tmp"] / "work" / "scripts" / "quality_gate.py").write_text("# old script\n")
    base2 = commit(world["work"], "old script")
    git(world["work"], "push", "-q", "origin", "main")
    git(world["work"], "checkout", "-q", "-b", "pr2", base2)
    write_pins(world["work"], pins_doc(go="go1.99.0"))
    head2 = commit(world["work"], "adds the mechanism")
    rec = run_ref(world, head2, mode="bootstrap", base_sha=base2)
    assert rec["status"] == "BOOTSTRAP"


def test_check_tools_prints_the_reference_and_pins_must_be_the_referenced_file(
    world, capsys
):
    branch(world)
    head = world["base"]
    rec = run_ref(world, head)
    ok = subprocess.run(
        [
            sys.executable,
            GATE,
            "check-tools",
            "--pins",
            rec["pins_file"],
            "--reference",
            str(world["tmp"] / "out" / "record.json"),
            "--surface",
            "coverage-go",
            "--tool",
            "go=go1.26.11",
        ],
        capture_output=True,
        text=True,
    )
    assert ok.returncode == 0, ok.stdout + ok.stderr
    assert f"reference-commit={world['base']}" in ok.stdout
    assert rec["reference_digest"] in ok.stdout
    # the pins file of the working tree is not the referenced one once it differs
    tampered = world["tmp"] / "tampered.json"
    doc = pins_doc(go="go1.0.0")
    tampered.write_text(json.dumps(doc))
    bad = subprocess.run(
        [
            sys.executable,
            GATE,
            "check-tools",
            "--pins",
            str(tampered),
            "--reference",
            str(world["tmp"] / "out" / "record.json"),
            "--surface",
            "coverage-go",
            "--tool",
            "go=go1.0.0",
        ],
        capture_output=True,
        text=True,
    )
    assert bad.returncode == 1 and "REFERENCE_MISMATCH" in bad.stderr


def test_check_tools_without_a_valid_reference_record_is_refused(world):
    bad = subprocess.run(
        [
            sys.executable,
            GATE,
            "check-tools",
            "--pins",
            os.path.join(world["work"], PINS),
            "--reference",
            str(world["tmp"] / "nope.json"),
            "--surface",
            "coverage-go",
            "--tool",
            "go=go1.26.11",
        ],
        capture_output=True,
        text=True,
    )
    assert bad.returncode == 1 and "REFERENCE_MISSING" in bad.stderr


# --- the wiring: no measuring job reads its pins or its checking script from the pull request's own tree ---

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "../.."))
WORKFLOWS = os.path.join(REPO, ".github/workflows")


def _workflow_texts():
    for name in sorted(os.listdir(WORKFLOWS)):
        if name.endswith(".yml"):
            yield name, open(os.path.join(WORKFLOWS, name)).read()


def test_no_workflow_reads_the_pins_or_the_checking_script_from_the_working_tree():
    sites = 0
    for name, text in _workflow_texts():
        assert "--pins .measurement-tools.json" not in text, name
        assert "--tool-pins .measurement-tools.json" not in text, name
        assert "scripts/quality_gate.py check-tools" not in text, name
        sites += text.count('"${MR_GATE:?') + text.count('--tool-pins "${MR_PINS:?')
    # 12 check-tools calls and the 2 object-evidence verifier calls: a new measuring call has to go through the reference
    assert sites == 13


def test_every_workflow_that_uses_the_reference_resolves_it_first_in_the_same_job():
    for name, text in _workflow_texts():
        if "MR_GATE" in text or "MR_PINS" in text:
            assert "uses: ./.github/actions/measurement-reference" in text, name


def test_the_action_takes_the_reference_from_the_base_and_the_script_from_the_base():
    text = open(
        os.path.join(REPO, ".github/actions/measurement-reference/action.yml")
    ).read()
    # the reference commit is the fetched base tip; the checking script is read from it, not from the working tree
    assert 'git fetch --no-tags --quiet origin "$BASE_REF"' in text
    assert 'git show "$TIP:scripts/quality_gate.py"' in text
    assert 'GATE="$REF/quality_gate.py"' in text
    # the base ref, the base and head SHA come from the event payload, never from an input of the pull request
    for var in ("BASE_REF", "BASE_SHA", "HEAD_SHA"):
        assert "github.event.pull_request." in text and var in text
    assert "inputs." not in text
    # outside a pull request the reference is the tree itself, and the record says so (SELF_REFERENCE)
    assert "--mode push" in text
    # the bootstrap is a one-time exception for the change that adds the mechanism; the script refuses it once the base has it
    assert (
        "MODE=bootstrap" in text
        and "BOOTSTRAP_REFUSED" in open(os.path.join(SCRIPTS, "quality_gate.py")).read()
    )
    for var in ("MR_GATE", "MR_PINS", "MR_RECORD"):
        assert f"{var}=" in text


def test_the_pin_file_is_part_of_the_approval_contract():
    doc = json.load(open(os.path.join(REPO, PINS)))
    assert doc["approvers"] and all(isinstance(a, str) and a for a in doc["approvers"])
    # the approvers are not tools or surfaces: the existing loader is unaffected
    assert set(qg.load_tool_pins(os.path.join(REPO, PINS))) == {"tools", "surfaces"}


def test_a_head_commit_missing_from_the_checkout_is_not_reported_as_a_deleted_pin(
    world,
):
    branch(world)
    run_ref(world, world["base"])  # positive control: the commit is there
    with pytest.raises(qg.GateError, match="REFERENCE_INVALID.*not available"):
        run_ref(world, "1" * 40)
