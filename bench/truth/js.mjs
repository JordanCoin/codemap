#!/usr/bin/env node
// JavaScript truth: file-level import edges resolved by Node's own resolver.
//
// Each file's module type is what Node would use: .mjs is ESM, .cjs is
// CommonJS, .js follows the nearest package.json "type" (default commonjs).
// Specifiers are extracted with the TypeScript parser in JS mode (a real
// parser, never a regex): import declarations, export ... from,
// require("literal") calls and import("literal") calls.
//   CommonJS files: require.resolve(spec, {paths: [dirname]}) via createRequire
//   ESM files:      import.meta.resolve(spec, parentURL), which needs the
//                   --experimental-import-meta-resolve flag on Node 22
// An edge is kept when the resolved path is an in-repo file outside
// node_modules (realpath applied) with a .js/.mjs/.cjs/.jsx/.ts/.tsx/.mts/.cts
// extension, excluding .d.ts and the file itself. Resolutions to .json or to
// Node builtins are out of scope.
//
// Scope: sources = every in-repo .js/.mjs/.cjs/.jsx file outside node_modules,
// dist, build, vendor, coverage and .git.
//
// usage: node --experimental-import-meta-resolve js.mjs <repo-root> <out-edges-file>
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL, fileURLToPath } from "node:url";

// typescript is pinned in bench/tools/package.json; resolve it from there.
const require = createRequire(new URL("../tools/package.json", import.meta.url));
const ts = require("typescript");

const [root0, outPath] = process.argv.slice(2);
if (!root0 || !outPath) {
  console.error("usage: js.mjs <repo-root> <out-edges-file>");
  process.exit(2);
}
const root = fs.realpathSync(root0);
const SKIP = new Set(["node_modules", ".git", "dist", "build", "vendor", "coverage"]);

function walk(dir, acc) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    if (SKIP.has(ent.name)) continue;
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) walk(p, acc);
    else if (ent.isFile() && /\.(js|mjs|cjs|jsx)$/.test(ent.name)) acc.push(p);
  }
  return acc;
}

const typeCache = new Map();
function packageType(dir) {
  if (typeCache.has(dir)) return typeCache.get(dir);
  let result = "commonjs";
  const pj = path.join(dir, "package.json");
  if (fs.existsSync(pj)) {
    try { const j = JSON.parse(fs.readFileSync(pj, "utf8")); result = j.type === "module" ? "module" : "commonjs"; } catch { /* malformed: default */ }
  } else if (dir !== root && dir.startsWith(root)) {
    result = packageType(path.dirname(dir));
  }
  typeCache.set(dir, result);
  return result;
}
function moduleType(file) {
  if (file.endsWith(".mjs")) return "module";
  if (file.endsWith(".cjs")) return "commonjs";
  return packageType(path.dirname(file));
}

const inRepo = (p) => {
  let real;
  try { real = fs.realpathSync(p); } catch { return null; }
  if (real !== root && !real.startsWith(root + path.sep)) return null;
  const rel = path.relative(root, real).split(path.sep).join("/");
  if (rel.split("/").includes("node_modules")) return null;
  return rel;
};
const isTarget = (rel) => /\.(js|mjs|cjs|jsx|ts|tsx|mts|cts)$/.test(rel) && !/\.d\.(c|m)?ts$/.test(rel);

function specifiers(sf) {
  const out = [];
  const visit = (node) => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      out.push({ spec: node.moduleSpecifier.text, kind: "esm" });
    } else if (ts.isCallExpression(node)) {
      const callee = node.expression;
      const arg = node.arguments[0];
      if (arg && ts.isStringLiteralLike(arg)) {
        if (callee.kind === ts.SyntaxKind.ImportKeyword) out.push({ spec: arg.text, kind: "esm" });
        else if (ts.isIdentifier(callee) && callee.text === "require") out.push({ spec: arg.text, kind: "cjs" });
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

const files = walk(root, []).sort();
const sources = new Set();
const edges = new Set();
const stats = { source_files: 0, parsed_files: 0, specifiers: 0, resolved_in_repo: 0, unresolved: 0, parse_errors: 0 };
const hasMetaResolve = typeof import.meta.resolve === "function";

for (const file of files) {
  const rel = inRepo(file);
  if (rel === null) continue;
  sources.add(rel);
  let text;
  try { text = fs.readFileSync(file, "utf8"); } catch { continue; }
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, file.endsWith(".jsx") ? ts.ScriptKind.JSX : ts.ScriptKind.JS);
  if (sf.parseDiagnostics && sf.parseDiagnostics.length) stats.parse_errors++;
  stats.parsed_files++;
  const mtype = moduleType(file);
  const req = createRequire(file);
  for (const { spec, kind } of specifiers(sf)) {
    stats.specifiers++;
    let resolved = null;
    try {
      if (kind === "cjs" || (mtype === "commonjs" && kind === "esm" && !hasMetaResolve)) {
        resolved = req.resolve(spec);
      } else if (kind === "esm" || mtype === "module") {
        if (!hasMetaResolve) throw new Error("import.meta.resolve unavailable: run with --experimental-import-meta-resolve");
        const url = import.meta.resolve(spec, pathToFileURL(file).href);
        if (!url.startsWith("file:")) throw new Error("not a file URL");
        resolved = fileURLToPath(url);
      }
    } catch {
      stats.unresolved++;
      continue;
    }
    if (!resolved) { stats.unresolved++; continue; }
    const dst = inRepo(resolved);
    if (dst === null || !isTarget(dst) || dst === rel) continue;
    edges.add(`${rel}\t${dst}`);
    stats.resolved_in_repo++;
  }
}
stats.source_files = sources.size;

fs.writeFileSync(outPath, [...edges].sort().map((e) => e + "\n").join(""));
fs.writeFileSync(outPath + ".files", [...sources].sort().map((f) => f + "\n").join(""));
const meta = { ...stats, edges: edges.size, typescript_parser: ts.version, node: process.version, import_meta_resolve: hasMetaResolve };
fs.writeFileSync(outPath + ".meta.json", JSON.stringify(meta, null, 1));
console.log(JSON.stringify(meta));
