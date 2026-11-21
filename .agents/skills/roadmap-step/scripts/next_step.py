import argparse
import re
import sys
from pathlib import Path


STATUSES = {"planned", "b5-pending", "verified"}
STEP_START = re.compile(r"^\|\s*\d+\s*\|")
STEP_ROW = re.compile(
    r"^\|\s*(\d{2})\s*\|\s*([^|]+)\|\s*([^|]+)\|\s*(.+)\|\s*$"
)


def next_step(markdown):
    last_verified = None
    upcoming = None
    expected = 1

    for line_number, line in enumerate(markdown.splitlines(), start=1):
        if not STEP_START.match(line):
            continue
        row = STEP_ROW.fullmatch(line)
        if row is None:
            raise ValueError(f"line {line_number}: malformed roadmap row")
        step, status, title, criterion = (cell.strip() for cell in row.groups())
        if int(step) != expected:
            raise ValueError(f"line {line_number}: expected step {expected:02d}, got {step}")
        if status not in STATUSES or not title or not criterion:
            raise ValueError(f"line {line_number}: invalid status, title or criterion for step {step}")
        if status == "verified":
            last_verified = step
        elif upcoming is None:
            upcoming = (step, status, title, criterion)
        expected += 1

    if expected == 1:
        raise ValueError("no roadmap steps found")
    return last_verified, upcoming


def main():
    parser = argparse.ArgumentParser(description="Show the first unfinished roadmap step.")
    parser.add_argument("roadmap", nargs="?", default="docs/roadmap.md", help="Markdown path or - for stdin")
    args = parser.parse_args()

    try:
        if args.roadmap == "-":
            markdown = sys.stdin.buffer.read().decode("utf-8-sig")
        else:
            markdown = Path(args.roadmap).read_text(encoding="utf-8-sig")
        last_verified, upcoming = next_step(markdown)
    except (OSError, ValueError) as error:
        parser.exit(2, f"{args.roadmap}: {error}\n")

    print(f"Last verified on B5: {last_verified or 'none'}")
    if upcoming is None:
        print("All roadmap steps are verified.")
        return
    step, status, title, criterion = upcoming
    print(f"Next: {step} [{status}] {title}")
    print(f"Acceptance: {criterion}")


if __name__ == "__main__":
    main()
