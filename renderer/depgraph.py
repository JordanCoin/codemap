"""
Dependency graph renderer - clean boxes with text-based dependency list.
"""

import os
import re
from collections import defaultdict


# Language compatibility groups - files can only depend on files in same group
LANG_GROUPS = {
    "python": "python",
    "go": "go",
    "javascript": "js",
    "typescript": "js",  # JS/TS can import each other
    "rust": "rust",
    "ruby": "ruby",
    "c": "c",
    "cpp": "c",  # C/C++ can include each other
    "java": "java",
    "swift": "swift",
    "bash": "bash",
    "kotlin": "kotlin",
    "c_sharp": "c_sharp",
    "php": "php",
    "dart": "dart",
    "r": "r",
}


def normalize_import(imp, lang):
    imp = imp.strip('"\'')
    if "/" in imp:
        imp = imp.split("/")[-1]
    if "." in imp and not imp.startswith("."):
        imp = imp.split(".")[-1]
    imp = re.sub(r'\.(py|go|js|ts|jsx|tsx|rb|rs|c|h|cpp|hpp|java|swift)$', '', imp)
    return imp.lower()


def find_internal_deps(files):
    """Find which files import which other files (same language group only)."""
    # Common stdlib module names that shouldn't match project files
    stdlib_names = {
        # Go stdlib
        "errors", "fmt", "io", "os", "path", "sync", "time", "context", "http",
        "net", "bytes", "strings", "strconv", "sort", "flag", "log", "bufio",
        "encoding", "testing", "runtime", "unsafe", "reflect", "regexp",
        # Python stdlib
        "logging", "typing", "collections", "datetime", "json", "sys", "re",
        "pathlib", "hashlib", "base64", "asyncio", "enum", "functools", "random",
        # JS/TS common
        "path", "fs", "util", "events", "stream", "crypto", "http", "https",
    }

    # Build lookup: name -> list of (path, language_group)
    # Multiple files can have the same basename
    name_to_infos = defaultdict(list)
    for f in files:
        path = f.get("path", "")
        lang = f.get("language", "")
        lang_group = LANG_GROUPS.get(lang, lang)
        basename = os.path.basename(path)
        name = re.sub(r'\.[^.]+$', '', basename).lower()
        name_to_infos[name].append((path, lang_group))

    deps = defaultdict(list)
    for f in files:
        path = f.get("path", "")
        src_lang = f.get("language", "")
        src_group = LANG_GROUPS.get(src_lang, src_lang)
        imports = f.get("imports") or []
        for imp in imports:
            # Skip if import looks like stdlib (single word, no path separators)
            if "/" not in imp and "." not in imp and imp.lower() in stdlib_names:
                continue

            norm = normalize_import(imp, src_lang)
            if norm in stdlib_names:
                continue  # Skip stdlib matches

            if norm in name_to_infos:
                # Find best match: same language group
                src_basename = os.path.basename(path)
                candidates = name_to_infos[norm]
                for target_path, target_group in candidates:
                    if target_path == path:
                        continue  # Skip self
                    if src_group != target_group:
                        continue  # Skip cross-language
                    target_name = os.path.basename(target_path)
                    # Skip same-basename matches (confusing when displayed)
                    if target_name == src_basename:
                        continue
                    if target_name not in deps[path]:
                        deps[path].append(target_name)

    return deps


def get_external_uses(f, internal_names):
    """Get external imports for a file."""
    imports = f.get("imports") or []
    # Common stdlib modules to filter out
    stdlib = {
        # Python
        "os", "re", "sys", "json", "math", "time", "collections", "defaultdict",
        "typing", "pathlib", "datetime", "hashlib", "base64", "asyncio", "enum",
        "logging", "contextlib", "functools", "itertools", "copy", "random",
        # Go
        "fmt", "io", "flag", "path", "strings", "embed", "runtime", "unsafe",
        "filepath", "context", "sync", "http", "net", "bytes", "errors",
        "strconv", "sort", "testing", "log", "bufio", "encoding",
        # JS/TS
        "react", "fs", "path", "util", "events", "stream", "crypto",
    }
    external = []
    seen = set()
    for imp in imports:
        norm = normalize_import(imp, f.get("language", ""))
        if norm not in internal_names and norm not in stdlib and len(norm) > 1:
            if norm not in seen:
                seen.add(norm)
                external.append(norm)
    return external  # Return all, no limit


def truncate_with_ellipsis(text, max_len):
    """Truncate text with ellipsis if too long."""
    if len(text) <= max_len:
        return text
    return text[:max_len - 2] + ".."


def create_box_lines(path, functions, external, width=34):
    """Create box content lines."""
    inner = width - 4
    lines = []

    # Filename - truncate if needed
    title = os.path.basename(path)
    lines.append(truncate_with_ellipsis(title, inner))
    lines.append("─" * inner)

    # Functions - show all, no truncation
    for fn in functions[:6]:
        lines.append(f"ƒ {fn}()")
    if len(functions) > 6:
        lines.append(f"  +{len(functions) - 6} more")

    # External deps - wrap to multiple lines if needed
    if external:
        lines.append("")
        prefix = "uses: "
        current_line = prefix
        for i, dep in enumerate(external):
            addition = dep if i == 0 else f", {dep}"
            if len(current_line) + len(addition) <= inner:
                current_line += addition
            else:
                lines.append(current_line)
                current_line = "      " + dep  # Indent continuation
        if current_line.strip():
            lines.append(current_line)

    return lines


def draw_box(lines, width=34):
    """Convert content lines to a box with borders."""
    result = []
    inner = width - 4
    result.append("┌" + "─" * (width - 2) + "┐")
    for line in lines:
        # Don't truncate content lines - let them show full info
        text = line[:inner].ljust(inner)
        result.append(f"│ {text} │")
    result.append("└" + "─" * (width - 2) + "┘")
    return result


def get_system_name(dir_path):
    """Infer a system/component name from directory path."""
    parts = dir_path.replace("\\", "/").split("/")
    # Filter out common non-descriptive parts
    skip = {"src", "lib", "app", "internal", "pkg", ".", ""}
    meaningful = [p for p in parts if p.lower() not in skip]
    if meaningful:
        return meaningful[0].title().replace("_", " ").replace("-", " ")
    return parts[-1].title() if parts else "Root"


def build_dependency_chains(files, internal_deps):
    """Build chains showing how files connect."""
    chains = []

    # Group files by system (top-level directory)
    systems = defaultdict(list)
    for f in files:
        path = f.get("path", "")
        parts = path.replace("\\", "/").split("/")
        system = parts[0] if parts else "."
        systems[system].append(f)

    return systems


def render(data, project_name):
    """Render dependency flow map."""
    files = data.get("files", [])
    external_deps_data = data.get("external_deps", {})

    if not files:
        print("  No source files found.")
        return

    # Build lookups
    internal_names = set()
    for f in files:
        basename = os.path.basename(f.get("path", ""))
        name = re.sub(r'\.[^.]+$', '', basename).lower()
        internal_names.add(name)

    internal_deps = find_internal_deps(files)

    # Count how many files depend on each file (for finding hubs)
    dep_counts = defaultdict(int)
    for src_path, targets in internal_deps.items():
        for target in targets:
            dep_counts[target] += 1

    # Group by top-level system
    systems = defaultdict(list)
    for f in files:
        path = f.get("path", "")
        parts = path.replace("\\", "/").split("/")
        system = parts[0] if len(parts) > 1 else "."
        systems[system].append(f)

    # Header with external deps
    print()

    # Build external deps by language
    ext_by_lang = {}
    if external_deps_data:
        for lang, deps in external_deps_data.items():
            if deps:
                names = []
                seen = set()
                for d in deps:
                    parts = d.split("/")
                    name = parts[-1]
                    if re.match(r'^v\d+$', name) and len(parts) > 1:
                        name = parts[-2]
                    if not re.match(r'^v\d+$', name) and len(name) > 1 and name not in seen:
                        seen.add(name)
                        names.append(name)
                if names:
                    ext_by_lang[lang] = names

    # Calculate box width
    title = f"{project_name} - Dependency Flow"
    max_width = len(title) + 6

    # Format dep lines
    dep_lines = []
    lang_labels = {
        "go": "Go", "javascript": "JS", "python": "Py", "rust": "Rs", "ruby": "Rb",
        "swift": "Swift", "bash": "Sh", "kotlin": "Kt", "c_sharp": "C#", "php": "PHP",
        "dart": "Dart", "r": "R"
    }
    for lang in ["go", "javascript", "python", "swift", "rust", "ruby", "bash", "kotlin", "c_sharp", "php", "dart", "r"]:
        if lang in ext_by_lang:
            label = lang_labels.get(lang, lang.title())
            deps_str = ", ".join(ext_by_lang[lang])
            line = f"{label}: {deps_str}"
            dep_lines.append(line)
            if len(line) + 4 > max_width:
                max_width = len(line) + 4

    # Cap width at 80
    max_width = min(max_width, 80)
    inner_width = max_width - 2  # Space between │ and │

    # Print box
    print(f"╭{'─' * inner_width}╮")
    # Title centered
    title_padded = title.center(inner_width)
    print(f"│{title_padded}│")

    if dep_lines:
        print(f"├{'─' * inner_width}┤")
        for line in dep_lines:
            # Wrap long lines
            content_width = inner_width - 2  # Account for padding spaces
            while len(line) > content_width:
                # Find break point
                break_at = line[:content_width].rfind(", ")
                if break_at == -1:
                    break_at = content_width - 1
                else:
                    break_at += 1  # Include the comma
                print(f"│ {line[:break_at]:<{content_width}} │")
                line = "    " + line[break_at:].lstrip(" ")
            print(f"│ {line:<{content_width}} │")

    print(f"╰{'─' * inner_width}╯")
    print()

    # Render each system
    for system in sorted(systems.keys()):
        sys_files = systems[system]
        system_name = get_system_name(system)

        # Check if this system has any deps or functions to show
        has_content = False
        for f in sys_files:
            if internal_deps.get(f.get("path", "")) or f.get("functions"):
                has_content = True
                break
        if not has_content:
            continue  # Skip empty systems

        # Section header
        print(f"{system_name} {'═' * (60 - len(system_name) - 1)}")

        # Build dependency lines for files in this system
        rendered = set()

        for f in sys_files:
            path = f.get("path", "")
            basename = os.path.basename(path)
            name_no_ext = re.sub(r'\.[^.]+$', '', basename)

            if basename in rendered:
                continue

            targets = internal_deps.get(path, [])

            if targets:
                # Group targets that share further deps
                target_names = [re.sub(r'\.[^.]+$', '', t) for t in targets]

                # Check if any targets have their own deps (for chaining)
                has_subdeps = []
                no_subdeps = []
                for t in targets:
                    t_path = None
                    for ff in files:
                        if os.path.basename(ff.get("path", "")) == t:
                            t_path = ff.get("path", "")
                            break
                    if t_path and internal_deps.get(t_path):
                        has_subdeps.append(t)
                    else:
                        no_subdeps.append(t)

                # Render the chain
                if len(targets) == 1:
                    t = targets[0]
                    t_name = re.sub(r'\.[^.]+$', '', t)
                    # Check for sub-deps
                    t_path = None
                    for ff in files:
                        if os.path.basename(ff.get("path", "")) == t:
                            t_path = ff.get("path", "")
                            break
                    sub_targets = internal_deps.get(t_path, []) if t_path else []
                    if sub_targets:
                        sub_names = [re.sub(r'\.[^.]+$', '', s) for s in sub_targets[:3]]
                        chain = f"{name_no_ext} ───▶ {t_name} ───▶ {', '.join(sub_names)}"
                        if len(sub_targets) > 3:
                            chain += f" +{len(sub_targets) - 3}"
                    else:
                        chain = f"{name_no_ext} ───▶ {t_name}"
                    print(f"  {chain}")
                else:
                    # Multiple targets - show branching
                    target_strs = [re.sub(r'\.[^.]+$', '', t) for t in targets]
                    if len(targets) <= 4:
                        print(f"  {name_no_ext} ───▶ {', '.join(target_strs)}")
                    else:
                        print(f"  {name_no_ext} ──┬──▶ {target_strs[0]}")
                        for t in target_strs[1:-1]:
                            print(f"  {' ' * len(name_no_ext)}   ├──▶ {t}")
                        print(f"  {' ' * len(name_no_ext)}   └──▶ {target_strs[-1]}")

                rendered.add(basename)

        # Count standalone files (have functions but no deps shown)
        standalone_count = 0
        for f in sys_files:
            path = f.get("path", "")
            basename = os.path.basename(path)
            funcs = f.get("functions") or []
            if basename not in rendered and funcs:
                standalone_count += 1

        if standalone_count > 0:
            print(f"  +{standalone_count} standalone files")

        print()

    # HUBS section - most depended-on files
    if dep_counts:
        hubs = sorted(dep_counts.items(), key=lambda x: -x[1])[:6]
        hubs = [(name, count) for name, count in hubs if count >= 2]
        if hubs:
            print("─" * 61)
            hub_strs = [f"{re.sub(r'.[^.]+$', '', name)} ({count}←)" for name, count in hubs]
            print(f"HUBS: {', '.join(hub_strs)}")

    # Summary
    total_funcs = sum(len(f.get("functions") or []) for f in files)
    internal_count = sum(len(t) for t in internal_deps.values())
    print(f"{len(files)} files · {total_funcs} functions · {internal_count} deps")
    print()
