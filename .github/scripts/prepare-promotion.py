"""Prepare a staging or production image promotion from a checkout of main."""

import os
from pathlib import Path
import re
import subprocess


TAG_PATTERN = re.compile(r"^(  RELAY_TAG: )(sha-[0-9a-f]{40})$", re.MULTILINE)


def read_tag(path):
    content = path.read_text()
    matches = list(TAG_PATTERN.finditer(content))
    if len(matches) != 1:
        raise ValueError(f"Expected exactly one full commit SHA tag in {path}")
    return content, matches[0].group(2)


def prepare():
    target = os.environ["TARGET_ENV"]
    if target not in ("stg", "prd"):
        raise ValueError("TARGET_ENV must be stg or prd")

    if target == "stg":
        sha = os.environ["IMAGE_SHA"]
        if not re.fullmatch(r"[0-9a-f]{40}", sha):
            raise ValueError("IMAGE_SHA must be a full commit SHA")
        # Match the image workflow's path filters. A slow or retried build must
        # not replace a promotion for a more recent source revision.
        latest = subprocess.check_output(
            [
                "git", "log", "-1", "--format=%H", "--first-parent", "HEAD", "--",
                ".", ":!deploy/**", ":!.doco-cd*.yml", ":!docs/**", ":!README.md",
            ],
            text=True,
        ).strip()
        if sha != latest:
            print("Skipping staging promotion for a superseded image build.")
            return {"promote": "false"}
        tag = f"sha-{sha}"
        path = Path(".doco-cd.yml")
        reason = "The image was published successfully by the main branch image workflow."
    else:
        # Production has no SHA input. Always promote the staging tag in main.
        _, tag = read_tag(Path(".doco-cd.yml"))
        path = Path(".doco-cd.prd.yml")
        reason = "The image tag was copied from the staging configuration on main."

    content, current = read_tag(path)
    if current == tag:
        # Still run the PR action so it can close an obsolete pending promotion.
        print("The target already references this image; reconcile any pending PR.")
    else:
        path.write_text(TAG_PATTERN.sub(lambda match: match.group(1) + tag, content))
    return {"promote": "true", "tag": tag, "file": str(path), "reason": reason}


if __name__ == "__main__":
    outputs = prepare()
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        for name, value in outputs.items():
            output.write(f"{name}={value}\n")
