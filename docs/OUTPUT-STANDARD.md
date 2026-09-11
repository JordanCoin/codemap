# Output standard

Every line codemap prints, posts, or renders is reviewed against four tests. A line that fails one does not ship. This applies to CLI output, hook output, MCP text, PR comments, the ledger, and any surface that renders codemap facts.

## The four tests

1. **Sourced.** The line names the path, line, PR, commit, or the command that reproduces it. If it cannot cite its source, it is an opinion, and codemap does not print opinions.
2. **Precise.** Numbers and names. Never "may", "might", "likely", "probably".
3. **Bounded.** The line states its own limit in the same breath: the coverage status, the cap that was hit, what was skipped.
4. **Useful.** The line names what to look at or what to do next. A fact with no next action is noise and is cut.

## Consequences

- A heuristic label does not ship until it is measured. A pair's "likelihood" is written to the calibration log and never printed. The printed line is the fact: `shared: src/email/emailService.ts (32 importers)`.
- An interpretation label comes after the number and states its threshold: `src/email/emailService.ts · 32 importers · blast high (high = 9+ importers or 2 hubs)`.
- No model writes a fact line. Agents consume codemap; they never produce its output.
- Third-party statistics do not appear on any codemap surface. Numbers from the user's own repositories do.
- Green says what it did not test. A passing result lists what was skipped.

## Review rule

A pull request that adds or changes a printed line quotes the new line in its description and says which of the four tests it passes and how. A reviewer who cannot find the source for a line asks for it or asks for the line to be removed.
