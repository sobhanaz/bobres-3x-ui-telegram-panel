# Branch ruleset for `main`

`protect-main.json` is the ruleset described in GitHub's
[About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets):

- `main` cannot be deleted or force-pushed;
- every change arrives through a pull request (no approval count, since the owner cannot
  approve their own PRs; review threads must be resolved);
- all seven CI jobs must pass on the latest commit before merging
  (`integration_id` 15368 is the GitHub Actions app, so only Actions can satisfy them);
- no bypass actors: the rules apply to administrators too. Loosen them from
  Settings → Rules if an emergency ever requires it.

Rulesets and branch protection are only available on **public repositories or GitHub Pro**.
This repository is private, so applying the file needs a plan upgrade first. Then either
import it in Settings → Rules → Rulesets → "New ruleset" → "Import a ruleset", or run:

```bash
gh api --method POST repos/sobhanaz/bobres-3x-ui-telegram-panel/rulesets --input .github/rulesets/protect-main.json
```

Keep the `required_status_checks` contexts in sync with the job `name:` fields in
`.github/workflows/ci.yml`; a renamed job would block every merge.
