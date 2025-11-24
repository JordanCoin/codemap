import json
import sys
import os
from collections import Counter
from rich.console import Console
from rich.tree import Tree
from rich.panel import Panel
from rich.table import Table
from rich.text import Text
from rich import box
from rich.columns import Columns

def load_data():
    """Load JSON data from stdin or file."""
    if len(sys.argv) > 1:
        try:
            with open(sys.argv[1], 'r') as f:
                return json.load(f)
        except FileNotFoundError:
            print(f"Error: File '{sys.argv[1]}' not found.")
            sys.exit(1)
        except json.JSONDecodeError:
            print(f"Error: Failed to decode JSON from '{sys.argv[1]}'.")
            sys.exit(1)
    else:
        # Read from stdin
        if sys.stdin.isatty():
            print("Usage: codemap-render <json_file> OR cat data.json | codemap-render")
            sys.exit(1)
        try:
            return json.load(sys.stdin)
        except json.JSONDecodeError:
            print("Error: Failed to decode JSON from stdin.")
            sys.exit(1)

def format_size(size):
    for unit in ['B', 'KB', 'MB', 'GB']:
        if size < 1024:
            return f"{size:.1f}{unit}"
        size /= 1024
    return f"{size:.1f}TB"

def get_dir_stats(node_dict):
    """Recursively calculate file count and total size for a directory node."""
    count = 0
    size = 0
    for key, value in node_dict.items():
        if isinstance(value, dict) and value.get('__is_file__'):
            count += 1
            size += value['data']['size']
        elif isinstance(value, dict):
            sub_count, sub_size = get_dir_stats(value)
            count += sub_count
            size += sub_size
    return count, size

def get_top_large_files(files):
    """
    Identify the top 5 largest source code files.
    Excludes common asset types.
    """
    # Extensions to ignore for "brain map" highlighting (assets, binaries, etc.)
    ASSET_EXTENSIONS = {
        '.png', '.jpg', '.jpeg', '.gif', '.svg', '.ico', '.webp',  # Images
        '.ttf', '.otf', '.woff', '.woff2', '.eot',                 # Fonts
        '.mp3', '.wav', '.mp4', '.mov',                            # Media
        '.zip', '.tar', '.gz', '.7z', '.rar',                      # Archives
        '.pdf', '.doc', '.docx', '.xls', '.xlsx',                  # Docs
        '.exe', '.dll', '.so', '.dylib', '.bin',                   # Binaries
        '.lock', '.resolved', '.sum',                              # Lockfiles
        '.map', '.css.map', '.js.map',                             # Source maps
        '.nib', '.xib', '.storyboard'                              # IB files (often large but not "logic")
    }
    
    source_files = [
        f for f in files 
        if f['ext'].lower() not in ASSET_EXTENSIONS
    ]
    
    # Sort by size descending
    source_files.sort(key=lambda x: x['size'], reverse=True)
    
    # Return set of paths for top 5
    return {f['path'] for f in source_files[:5]}

def build_tree(tree, files):
    """
    Organize files into a tree structure.
    files is a list of dicts: {'path': '...', 'size': ..., 'ext': ...}
    """
    # Identify top large files for highlighting
    top_large_files = get_top_large_files(files)

    # Build a nested dictionary structure first
    root_dict = {}
    
    for file in files:
        parts = file['path'].split(os.sep)
        current = root_dict
        for i, part in enumerate(parts):
            if i == len(parts) - 1:
                # It's a file
                current[part] = {'__is_file__': True, 'data': file}
            else:
                # It's a directory
                if part not in current:
                    current[part] = {}
                current = current[part]
                
    # Recursively add to rich Tree
    def add_nodes(current_node, current_dict):
        # Separate files and directories
        dirs = []
        files = []
        
        for name, node_data in current_dict.items():
            is_file = isinstance(node_data, dict) and node_data.get('__is_file__')
            if is_file:
                files.append((name, node_data['data']))
            else:
                dirs.append((name, node_data))
        
        # Sort directories and files
        dirs.sort(key=lambda x: x[0])
        files.sort(key=lambda x: x[0])
        
        # Add directories first
        for name, node_data in dirs:
            # If this directory has exactly 1 child which is a directory (and no files), merge.
            merged_name = name
            merged_data = node_data
            
            while True:
                sub_dirs = []
                sub_files = []
                for k, v in merged_data.items():
                    if isinstance(v, dict) and v.get('__is_file__'):
                        sub_files.append(k)
                    else:
                        sub_dirs.append((k, v))
                
                if len(sub_files) == 0 and len(sub_dirs) == 1:
                    # Flatten!
                    sub_name, sub_data = sub_dirs[0]
                    merged_name = f"{merged_name}/{sub_name}"
                    merged_data = sub_data
                else:
                    break
            
            # Calculate stats for this (possibly merged) directory
            file_count, total_size = get_dir_stats(merged_data)
            
            # Check for homogeneous extensions in the immediate children of the merged dir            
            immediate_files = []
            for k, v in merged_data.items():
                if isinstance(v, dict) and v.get('__is_file__'):
                    immediate_files.append(v['data'])
            
            common_ext = None
            if len(immediate_files) > 1:
                exts = {f['ext'] for f in immediate_files}
                if len(exts) == 1:
                    common_ext = exts.pop()

            # Format Stats
            stats_parts = []
            if file_count == 1:
                # Single file: just size
                stats_parts.append(format_size(total_size))
            else:
                stats_parts.append(f"{file_count} files")
                stats_parts.append(format_size(total_size))
            
            if common_ext:
                stats_parts.append(f"all {common_ext}")
            
            stats_str = f"({', '.join(stats_parts)})"
            
            branch = current_node.add(f"[bold blue]📂 {merged_name}/[/] [dim]{stats_str}[/]")

            add_nodes(branch, merged_data, strip_ext=common_ext)
            
    # Update add_nodes signature to support recursion with stripping
    def add_nodes_wrapper(current_node, current_dict, strip_ext=None):
        # Separate files and directories
        dirs = []
        files = []
        
        for name, node_data in current_dict.items():
            is_file = isinstance(node_data, dict) and node_data.get('__is_file__')
            if is_file:
                files.append((name, node_data['data']))
            else:
                dirs.append((name, node_data))
        
        # Sort directories and files
        dirs.sort(key=lambda x: x[0])
        files.sort(key=lambda x: x[0])
        
        # Add directories first
        for name, node_data in dirs:
            # Flattening Logic
            merged_name = name
            merged_data = node_data
            
            while True:
                sub_dirs = []
                sub_files = []
                for k, v in merged_data.items():
                    if isinstance(v, dict) and v.get('__is_file__'):
                        sub_files.append(k)
                    else:
                        sub_dirs.append((k, v))
                
                if len(sub_files) == 0 and len(sub_dirs) == 1:
                    sub_name, sub_data = sub_dirs[0]
                    merged_name = f"{merged_name}/{sub_name}"
                    merged_data = sub_data
                else:
                    break
            
            # Stats & Homogeneous Check
            file_count, total_size = get_dir_stats(merged_data)
            
            immediate_files = []
            for k, v in merged_data.items():
                if isinstance(v, dict) and v.get('__is_file__'):
                    immediate_files.append(v['data'])
            
            common_ext = None
            if len(immediate_files) > 1:
                exts = {f['ext'] for f in immediate_files}
                if len(exts) == 1:
                    common_ext = exts.pop()

            # Format Stats
            stats_parts = []
            if file_count == 1:
                stats_parts.append(format_size(total_size))
            else:
                stats_parts.append(f"{file_count} files")
                stats_parts.append(format_size(total_size))
            
            if common_ext:
                stats_parts.append(f"all {common_ext}")
            
            stats_str = f"({', '.join(stats_parts)})"
            
            branch = current_node.add(f"[bold blue] {merged_name}/[/] [dim]{stats_str}[/]")
            add_nodes_wrapper(branch, merged_data, strip_ext=common_ext)
            
        # Add files as a grid (Columns) if any exist
        if files:
            file_renderables = []
            for name, data in files:
                ext = data['ext']
                path = data['path']
                
                display_name = name
                if strip_ext and name.endswith(strip_ext):
                    display_name = name[:-len(strip_ext)]
                
                # Color mapping
                style = "white"
                # Cyan
                if ext in ['.go', '.mod', '.sum', '.dart']: style = "cyan"
                # Yellow
                elif ext in ['.py', '.pyc', '.pyd', '.venv', '.js', '.ts', '.jsx', '.tsx', '.mjs', '.cjs', '.vue', '.svelte', '.pl', '.pm', '.t', '.sql', '.db', '.sqlite']: style = "yellow"
                # Magenta
                elif ext in ['.html', '.css', '.scss', '.sass', '.less', '.php', '.phtml', '.hs', '.lhs', '.tf', '.tfvars', '.hcl']: style = "magenta"
                # Green
                elif ext in ['.md', '.txt', '.rst', '.adoc']: style = "green"
                # Red
                elif ext in ['.json', '.yaml', '.yml', '.toml', '.xml', '.csv', '.ini', '.conf', '.env', '.rb', '.erb', '.gemfile', '.gemspec']: style = "red"
                # Bold White
                elif ext in ['.sh', 'Makefile', 'Dockerfile', 'dockerfile', '.bat', '.ps1']: style = "bold white"
                # Bold Red
                elif ext in ['.swift', '.kt', '.java', '.kotlin', '.scala', '.groovy', '.rs', '.rlib']: style = "bold red"
                # Bold Blue
                elif ext in ['.c', '.cpp', '.h', '.hpp', '.cc', '.cxx', '.m', '.mm', '.cs', '.fs', '.vb']: style = "bold blue"
                # Blue
                elif ext in ['.lua', '.r', '.rmd']: style = "blue"
                # Dim White
                elif ext in ['.dockerignore', '.gitignore', '.gitattributes']: style = "dim white"
                
                # Highlight if in top 5 large files
                prefix = ""
                if path in top_large_files:
                    prefix = "⭐️ "
                    style = f"bold {style}"
                
                file_renderables.append(Text(f"{prefix}{display_name}", style=style))
            
            current_node.add(Columns(file_renderables, expand=False, equal=True, column_first=True))

    add_nodes_wrapper(tree, root_dict)

def main():
    data = load_data()
    
    root_path = data.get('root', 'Project')
    project_name = os.path.basename(root_path)
    files = data.get('files', [])
    
    # Stats
    total_files = len(files)
    total_size = sum(f['size'] for f in files)
    extensions = Counter(f['ext'] for f in files if f['ext'])
    top_exts = extensions.most_common(5)
    
    console = Console()
    
    # Header
    stats_text = f"Files: [bold]{total_files}[/] | Size: [bold]{format_size(total_size)}[/]\n"
    if top_exts:
        stats_text += "Top Extensions: " + ", ".join([f"{ext} ({count})" for ext, count in top_exts])
    
    header = Panel(
        stats_text,
        title=f"[bold green]{project_name}[/]",
        border_style="green",
        box=box.ROUNDED,
        expand=False
    )
    
    console.print(header)
    
    # Tree
    tree = Tree(f"[bold]{project_name}[/]")
    build_tree(tree, files)
    console.print(tree)

if __name__ == "__main__":
    main()
