//! Lists, for one Rust source file, every syntactic reference the benchmark
//! resolves with rust-analyzer: `mod` declarations, `use` tree leaves and
//! multi-segment paths in expressions and types, plus `include!`-family
//! macro literals. Output is JSON on stdout. Positions are 0-based line and
//! UTF-16 column as the Language Server Protocol expects.
//!
//! usage: rustsyn <file.rs>
use proc_macro2::{LineColumn, Span};
use serde::Serialize;
use syn::visit::Visit;
use syn::{Item, ItemMod, Path, UseTree};

#[derive(Serialize)]
struct Pos {
    line: u32,
    character: u32,
}

#[derive(Serialize)]
struct Segment {
    name: String,
    pos: Pos,
}

#[derive(Serialize)]
struct Reference {
    kind: &'static str,
    segments: Vec<Segment>,
    #[serde(skip_serializing_if = "Option::is_none")]
    literal: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    path_attr: Option<String>,
}

#[derive(Serialize)]
struct Output {
    file: String,
    parse_error: Option<String>,
    references: Vec<Reference>,
}

struct Collector<'a> {
    lines: &'a [&'a str],
    refs: Vec<Reference>,
}

impl<'a> Collector<'a> {
    fn pos(&self, lc: LineColumn) -> Pos {
        // proc_macro2 columns are UTF-8 byte offsets... no: they are char
        // offsets; convert to UTF-16 code units for LSP.
        let line = self.lines.get(lc.line.saturating_sub(1)).copied().unwrap_or("");
        let utf16: usize = line.chars().take(lc.column).map(|c| c.len_utf16()).sum();
        Pos { line: (lc.line.saturating_sub(1)) as u32, character: utf16 as u32 }
    }
    fn span_pos(&self, span: Span) -> Pos {
        self.pos(span.start())
    }
    fn push_path(&mut self, kind: &'static str, path: &Path) {
        if path.segments.len() < 2 && kind != "use" {
            return;
        }
        let segments = path
            .segments
            .iter()
            .map(|s| Segment { name: s.ident.to_string(), pos: self.span_pos(s.ident.span()) })
            .collect();
        self.refs.push(Reference { kind, segments, literal: None, path_attr: None });
    }
    fn walk_use(&mut self, tree: &UseTree, prefix: &mut Vec<Segment>) {
        match tree {
            UseTree::Path(p) => {
                prefix.push(Segment { name: p.ident.to_string(), pos: self.span_pos(p.ident.span()) });
                self.walk_use(&p.tree, prefix);
                prefix.pop();
            }
            UseTree::Name(n) => {
                let mut segs: Vec<Segment> = prefix.iter().map(|s| Segment { name: s.name.clone(), pos: Pos { line: s.pos.line, character: s.pos.character } }).collect();
                segs.push(Segment { name: n.ident.to_string(), pos: self.span_pos(n.ident.span()) });
                self.refs.push(Reference { kind: "use", segments: segs, literal: None, path_attr: None });
            }
            UseTree::Rename(r) => {
                let mut segs: Vec<Segment> = prefix.iter().map(|s| Segment { name: s.name.clone(), pos: Pos { line: s.pos.line, character: s.pos.character } }).collect();
                segs.push(Segment { name: r.ident.to_string(), pos: self.span_pos(r.ident.span()) });
                self.refs.push(Reference { kind: "use", segments: segs, literal: None, path_attr: None });
            }
            UseTree::Glob(_) => {
                let segs: Vec<Segment> = prefix.iter().map(|s| Segment { name: s.name.clone(), pos: Pos { line: s.pos.line, character: s.pos.character } }).collect();
                if !segs.is_empty() {
                    self.refs.push(Reference { kind: "use-glob", segments: segs, literal: None, path_attr: None });
                }
            }
            UseTree::Group(g) => {
                for t in &g.items {
                    self.walk_use(t, prefix);
                }
            }
        }
    }
}

fn path_attr(item: &ItemMod) -> Option<String> {
    for attr in &item.attrs {
        if attr.path().is_ident("path") {
            if let syn::Meta::NameValue(nv) = &attr.meta {
                if let syn::Expr::Lit(syn::ExprLit { lit: syn::Lit::Str(s), .. }) = &nv.value {
                    return Some(s.value());
                }
            }
        }
    }
    None
}

impl<'ast, 'a> Visit<'ast> for Collector<'a> {
    fn visit_item(&mut self, item: &'ast Item) {
        match item {
            Item::Mod(m) if m.content.is_none() => {
                let seg = Segment { name: m.ident.to_string(), pos: self.span_pos(m.ident.span()) };
                self.refs.push(Reference { kind: "mod", segments: vec![seg], literal: None, path_attr: path_attr(m) });
            }
            Item::Use(u) => {
                let mut prefix = Vec::new();
                self.walk_use(&u.tree, &mut prefix);
            }
            _ => {}
        }
        syn::visit::visit_item(self, item);
    }
    fn visit_path(&mut self, path: &'ast Path) {
        self.push_path("path", path);
        syn::visit::visit_path(self, path);
    }
    fn visit_macro(&mut self, mac: &'ast syn::Macro) {
        let name = mac.path.segments.last().map(|s| s.ident.to_string()).unwrap_or_default();
        if matches!(name.as_str(), "include" | "include_str" | "include_bytes") {
            if let Ok(lit) = mac.parse_body::<syn::LitStr>() {
                let seg = Segment { name: name.clone(), pos: self.span_pos(mac.path.segments.last().unwrap().ident.span()) };
                self.refs.push(Reference { kind: "include", segments: vec![seg], literal: Some(lit.value()), path_attr: None });
            }
        }
        syn::visit::visit_macro(self, mac);
    }
}

fn main() {
    let file = std::env::args().nth(1).expect("usage: rustsyn <file.rs>");
    let src = std::fs::read_to_string(&file).expect("read");
    let lines: Vec<&str> = src.split('\n').collect();
    let mut out = Output { file: file.clone(), parse_error: None, references: Vec::new() };
    match syn::parse_file(&src) {
        Ok(ast) => {
            let mut c = Collector { lines: &lines, refs: Vec::new() };
            c.visit_file(&ast);
            out.references = c.refs;
        }
        Err(e) => out.parse_error = Some(e.to_string()),
    }
    println!("{}", serde_json::to_string(&out).unwrap());
}
