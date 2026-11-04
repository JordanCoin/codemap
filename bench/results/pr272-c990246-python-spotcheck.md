# PR #272 at c990246 (fix/python-package-members, first commit) against the v4.6.4 Python truth

This is the run on the PR's first commit. The run on the PR head 8500403, which adds the verifier fixes, is in `results/pr272-8500403-python-spotcheck.md`; both are kept, labelled by SHA.

Same pinned SHAs, same truth files as `results/v4.6.4.md` (byte-identical `truth.edges` for all five repositories, checked with `cmp`), same default configuration. codemap was built from the PR head (`go build`, reports `codemap dev`) with the release's ast-grep 0.42.1 on `PATH`. No truth generator was changed for this run. Generated tables: `results/pr272-c990246-python.md` and `.json`.

## Before and after, per repository

| group | repo | n truth | v4.6.4 tp / fn / fp | v4.6.4 recall | v4.6.4 precision | PR 272 tp / fn / fp | PR 272 recall [95% CI] | PR 272 precision [95% CI] |
|---|---|---:|---|---|---|---|---|---|
| held-out | yt-dlp/yt-dlp | 3831 | 3788 / 43 / 24 | 0.989 | 0.994 | 3816 / 15 / 1 | 0.996 [0.994, 0.998] | 1.000 [0.999, 1.000] |
| held-out | AUTOMATIC1111/stable-diffusion-webui | 864 | 864 / 0 / 19240 | 1.000 | 0.043 | 863 / 1 / 13 | 0.999 [0.993, 1.000] | 0.985 [0.975, 0.991] |
| held-out | github/spec-kit | 2431 | 2171 / 260 / 77 | 0.893 | 0.966 | 2431 / 0 / 1 | 1.000 [0.998, 1.000] | 1.000 [0.998, 1.000] |
| tuned | pytest-dev/pytest | 856 | 822 / 34 / 28 | 0.960 | 0.967 | 852 / 4 / 1 | 0.995 [0.988, 0.998] | 0.999 [0.993, 1.000] |
| tuned | pallets/werkzeug | 400 | 384 / 16 / 61 | 0.960 | 0.863 | 400 / 0 / 48 | 1.000 [0.990, 1.000] | 0.893 [0.861, 0.918] |

Pooled held-out Python (counts summed over yt-dlp, stable-diffusion-webui, spec-kit):

| build | n truth | n codemap | tp | recall [95% CI] | precision [95% CI] |
|---|---:|---:|---:|---|---|
| v4.6.4 | 7126 | 26164 | 6823 | 0.957 [0.953, 0.962] | 0.261 [0.255, 0.266] |
| PR 272 | 7126 | 7125 | 7110 | 0.998 [0.996, 0.999] | 0.998 [0.997, 0.999] |

Pooled tuned Python: v4.6.4 1206 of 1256 recall 0.960, precision 0.931 (1295 codemap edges); PR 272 1252 of 1256 recall 0.997, precision 0.962 (1301 codemap edges).

## What changed edge by edge

Computed as set differences between the two codemap edge sets against the same truth.

| repo | false negatives fixed | false positives fixed | new false negatives (were true positives) | new false positives |
|---|---:|---:|---:|---:|
| yt-dlp/yt-dlp | 28 | 23 | 0 | 0 |
| AUTOMATIC1111/stable-diffusion-webui | 0 | 19228 | 1 | 1 |
| github/spec-kit | 260 | 76 | 0 | 0 |
| pytest-dev/pytest | 30 | 27 | 0 | 0 |
| pallets/werkzeug | 16 | 15 | 0 | 2 |

The fixes are the two patterns the PR targets: `from <namespace package> import a, b` no longer fans out to the directory (stable-diffusion-webui), and `from pkg import name` now resolves to `pkg/name.py` when `name` is a submodule and to `pkg/__init__.py` when it is a member defined there (spec-kit's 260 `from . import name` / `from .. import name` false negatives, yt-dlp's and pytest's `__init__.py` false positives).

### New disagreements introduced by the change (4 edges)

- stable-diffusion-webui, `modules/sd_samplers_timesteps_impl.py`: `from modules.models.diffusion.uni_pc import uni_pc`. PR 272 now resolves it to `modules/models/diffusion/uni_pc/uni_pc.py`, which is correct: `uni_pc.py` is a submodule of that package. Truth says `uni_pc/__init__.py`, and that is a truth error, reported below. Counted as one new false negative plus one new false positive against the unchanged truth.
- werkzeug, `examples/coolmagic/utils.py` -> `examples/coolmagic/helpers.py` (`from coolmagic import helpers`) and `examples/i18nurls/application.py` -> `examples/i18nurls/views.py` (`from i18nurls import views`). PR 272 resolves the member as a submodule, which is what those statements import when the example runs with `examples/` on `sys.path` (its `manage-*.py` scripts live there). Truth cannot resolve `coolmagic` or `i18nurls` at all (only `examples.coolmagic` is reachable from the repo root), so both count as false positives; same truth limitation as the 46 pre-existing werkzeug false positives below.

No edge that was a true positive under v4.6.4 became wrong under PR 272 except the uni_pc one, where the truth is at fault.

### Remaining disagreements under PR 272 (not introduced by the change)

- yt-dlp, 15 false negatives: 11 are in `test/testdata/**` plugin fixtures (`from yt_dlp.extractor.common import InfoExtractor`): codemap's walker skips `testdata` directories by design (Go toolchain convention, `scanner/walker.go`), truth includes them because they are `.py` files the interpreter can import. Scope ambiguity, pre-existing. The other 4 target `vendor/__init__.py` and `extractor/generic.py` and were false negatives under v4.6.4 too.
- yt-dlp, 1 false positive: `jsc/_builtin/ejs.py`, `from yt_dlp.extractor.youtube.jsc._builtin import vendor`: `vendor` is a subpackage (`vendor/__init__.py`); codemap resolves to `_builtin/__init__.py`. codemap wrong: the member-is-a-package case is not covered by the fix. Pre-existing.
- stable-diffusion-webui, 12 false positives: extension scripts importing siblings by bare name (`from ldsr_model_arch import LDSR`, `import lora_patches` in `extensions-builtin/*/scripts/*.py`); webui adds each extension directory to `sys.path` at runtime, which truth does not model. Scope ambiguity, pre-existing (same 12 under v4.6.4).
- spec-kit, 1 false positive: `import git_common` after a `sys.path.insert` in a test. Scope ambiguity, pre-existing.
- pytest, 4 false negatives and 1 false positive: `src/_pytest/_version.py` is generated by setuptools-scm at install time and gitignored, so codemap does not see it (2 FN); `from py import error` and `from py.path import local` go through the `src/py.py` shim that aliases `py.path` to `_pytest._py.path` at import, so truth says `_pytest/_py/*.py` and codemap says `src/py.py` (2 FN, 1 FP). Scope ambiguity, pre-existing.
- werkzeug, 46 false positives besides the 2 new ones: relative and package-qualified imports inside `examples/<app>/` packages (`from .application import make_app`, `from coolmagic.utils import export`). Truth cannot resolve them because `examples/` is not on `sys.path` from the repo root (only the `manage-*.py` scripts there get the script-directory rule). codemap's answers are the files those statements import when the examples run. Reported below as a truth limitation.

## Agreeing edges checked against the source (5 per repo, seed 20261103)

Drawn by `bench/spotcheck_agree.py` from the true-positive set of the PR 272 run, then the import statement was read in the source file. All 25 are genuine: the statement names the target module (or a member defined in the target `__init__.py`).

| repo | source -> target | statement in source | confirmed |
|---|---|---|---|
| yt-dlp | `yt_dlp/extractor/_extractors.py` -> `yt_dlp/extractor/apa.py` | `from .apa import APAIE` (line 99) | yes |
| yt-dlp | `yt_dlp/extractor/_extractors.py` -> `yt_dlp/extractor/funker530.py` | `from .funker530 import Funker530IE` (633) | yes |
| yt-dlp | `yt_dlp/extractor/_extractors.py` -> `yt_dlp/extractor/internazionale.py` | `from .internazionale import InternazionaleIE` (797) | yes |
| yt-dlp | `yt_dlp/extractor/_extractors.py` -> `yt_dlp/extractor/islamchannel.py` | `from .islamchannel import (` (807) | yes |
| yt-dlp | `yt_dlp/extractor/_extractors.py` -> `yt_dlp/extractor/myspass.py` | `from .myspass import MySpassIE` (1119) | yes |
| stable-diffusion-webui | `modules/textual_inversion/textual_inversion.py` -> `modules/shared.py` | `from modules import shared, devices, sd_hijack, sd_models, images, sd_samplers, ...` (15); `modules/` is a namespace package, `shared` is `modules/shared.py` | yes |
| stable-diffusion-webui | `modules/textual_inversion/textual_inversion.py` -> `modules/sd_hijack.py` | same statement, `sd_hijack` is `modules/sd_hijack.py` | yes |
| stable-diffusion-webui | `scripts/xyz_grid.py` -> `modules/errors.py` | `from modules import images, sd_samplers, processing, sd_models, sd_vae, sd_schedulers, errors` (14) | yes |
| stable-diffusion-webui | `modules/api/api.py` -> `modules/textual_inversion/textual_inversion.py` | `from modules.textual_inversion.textual_inversion import create_embedding, train_embedding` (24) | yes |
| stable-diffusion-webui | `extensions-builtin/Lora/preload.py` -> `modules/paths.py` | `from modules import paths` (2) | yes |
| spec-kit | `tests/specify_cli/workflows/step/test_command_remove.py` -> `src/specify_cli/workflows/step/command_remove.py` | `from specify_cli.workflows.step.command_remove import workflow_step_remove` (73, inside a test function) | yes |
| spec-kit | `src/specify_cli/workflows/command_definition.py` -> `src/specify_cli/workflows/_storage.py` | `from ._storage import WorkflowStorageError, validate_workflow_storage` (10) | yes |
| spec-kit | `tests/specify_cli/workflows/step/test_command_search.py` -> `src/specify_cli/workflows/step/catalog/__init__.py` | `from specify_cli.workflows.step.catalog import StepCatalog` (23); `StepCatalog` is a member of the package | yes |
| spec-kit | `tests/specify_cli/test_command_init.py` -> `src/specify_cli/integrations/generic/__init__.py` | `from specify_cli.integrations.generic import GenericIntegration` (246) | yes |
| spec-kit | `src/specify_cli/extensions/catalog/command_list.py` -> `src/specify_cli/extensions/__init__.py` | `from .. import ExtensionCatalog, ValidationError` (21); both are defined in `extensions/__init__.py` | yes |
| pytest | `src/_pytest/hookspec.py` -> `src/_pytest/_code/code.py` | `from _pytest._code.code import ExceptionInfo` (22, under `TYPE_CHECKING`) | yes |
| pytest | `src/pytest/__init__.py` -> `src/_pytest/monkeypatch.py` | `from _pytest.monkeypatch import MonkeyPatch` (46) | yes |
| pytest | `testing/test_reports.py` -> `src/_pytest/pytester.py` | `from _pytest.pytester import Pytester` (10) | yes |
| pytest | `src/_pytest/junitxml.py` -> `src/_pytest/_code/code.py` | `from _pytest._code.code import ExceptionRepr` (23) | yes |
| pytest | `src/_pytest/reports.py` -> `src/_pytest/config/__init__.py` | `from _pytest.config import Config` (32); `Config` is defined in the package `__init__.py` | yes |
| werkzeug | `tests/test_apply_conditions.py` -> `src/werkzeug/datastructures/headers.py` | `from werkzeug.datastructures.headers import Headers` (9) | yes |
| werkzeug | `src/werkzeug/test.py` -> `src/werkzeug/utils.py` | `from .utils import _get_content_type` (43) | yes |
| werkzeug | `src/werkzeug/__init__.py` -> `src/werkzeug/serving.py` | `from .serving import run_simple as run_simple` (1) | yes |
| werkzeug | `src/werkzeug/formparser.py` -> `src/werkzeug/sansio/multipart.py` | `from .sansio.multipart import Data` (17) | yes |
| werkzeug | `src/werkzeug/http.py` -> `src/werkzeug/sansio/http.py` | `from .sansio import http` (1142, inside a function); `http` is the submodule `sansio/http.py` | yes |

The v4.6.4 true-positive set gives the same picture: its sample (same seed) was also 25 of 25 genuine, including `scripts/sd_upscale.py` -> `modules/shared.py` (`from modules.shared import opts, state`) and `src/_pytest/runner.py` -> `src/_pytest/outcomes.py` (`from _pytest.outcomes import Exit`).

## Truth problems found, reported and left unchanged (as requested)

1. **Submodule check fails when the parent package cannot be imported.** `bench/truth/py_walk.py` decides whether `from a.b import c` names a submodule with `importlib.util.find_spec("a.b.c")`, which imports `a.b` first. When `a.b/__init__.py` imports a dependency that is not installed (stable-diffusion-webui is not pip-installable, so `torch` is absent and `uni_pc/__init__.py` does `from .sampler import UniPCSampler`, which imports torch), `find_spec` raises `ModuleNotFoundError`, the generator treats the name as a member, and the edge goes to `uni_pc/__init__.py` instead of `uni_pc/uni_pc.py`. One edge in this run; PR 272's answer is the correct one. A fix would fall back to a filesystem check (`PathFinder.find_spec` on the package's `__path__`) when the parent import fails; not applied here.
2. **Example applications run from a directory that is not the repo root.** werkzeug's `examples/<app>/` packages are run by `examples/manage-<app>.py`, which puts `examples/` on `sys.path`; the generator applies the script-directory rule only to the script file itself, so the 48 relative and package-qualified imports inside those packages (`from .application import make_app`, `from coolmagic import helpers`) are unresolvable in truth and count against codemap, which resolves them correctly. A fix would extend the script-directory rule to packages that a sibling script imports; not applied here. This is the whole of werkzeug's 0.893 precision.
3. **Pre-existing scope choices that are not errors but are worth knowing:** codemap skips `testdata` directories (11 yt-dlp false negatives); setuptools-scm's generated `_version.py` is gitignored and not seen by codemap (2 pytest false negatives); runtime `sys.path` manipulation (webui extensions, spec-kit's test, pytest's `py` shim) is modelled by neither side consistently.
