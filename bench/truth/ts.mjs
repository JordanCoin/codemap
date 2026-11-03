#!/usr/bin/env node
// TypeScript truth: file-level import edges resolved by the TypeScript
// compiler API with each tsconfig's own options (moduleResolution, paths,
// baseUrl, rootDirs, extends), exactly as `tsc` would resolve them.
//
// Scope (see bench/README.md):
//   sources  = every file listed by some tsconfig.json in the repo (outside
//              node_modules), excluding .d.ts files. A file listed by more
//              than one tsconfig is resolved under each of them and the
//              edges are unioned: every configuration that compiles the file
//              resolves its imports, and a monorepo's root tsconfig (with
//              `paths` to package sources) and a package's own tsconfig
//              (resolving through node_modules to built declarations) can
//              legitimately disagree.
//   edges    = import declarations, export ... from, import x = require(),
//              require("literal") calls and import("literal") calls whose
//              specifier the compiler resolves to an in-repo file that is not
//              under node_modules (realpath applied, so a workspace package
//              linked through node_modules counts as in-repo) and is not a
//              .d.ts file. Type-only imports are included. Triple-slash
//              references are excluded.
//
// usage: node ts.mjs <repo-root> <out-edges-file>
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

// typescript is pinned in bench/tools/package.json; resolve it from there.
const require = createRequire(new URL("../tools/package.json", import.meta.url));
const ts = require("typescript");

const [root0, outPath] = process.argv.slice(2);
if (!root0 || !outPath) {
  console.error("usage: ts.mjs <repo-root> <out-edges-file>");
  process.exit(2);
}
const root = fs.realpathSync(root0);

function walkConfigs(dir, acc) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    if (ent.name === "node_modules" || ent.name === ".git" || ent.name === "dist" || ent.name === "build") continue;
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) walkConfigs(p, acc);
    else if (ent.isFile() && /^tsconfig(\..+)?\.json$/.test(ent.name)) acc.push(p);
  }
  return acc;
}

const configs = walkConfigs(root, []).sort();
const inRepo = (p) => {
  let real;
  try { real = fs.realpathSync(p); } catch { return null; }
  if (real !== root && !real.startsWith(root + path.sep)) return null;
  const rel = path.relative(root, real).split(path.sep).join("/");
  if (rel.split("/").includes("node_modules")) return null;
  return rel;
};
const isDts = (p) => /\.d\.(c|m)?ts$/.test(p);

function specifiers(sf) {
  const out = [];
  const visit = (node) => {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      out.push(node.moduleSpecifier.text);
    } else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference) && ts.isStringLiteral(node.moduleReference.expression)) {
      out.push(node.moduleReference.expression.text);
    } else if (ts.isCallExpression(node)) {
      const callee = node.expression;
      const arg = node.arguments[0];
      if (arg && ts.isStringLiteralLike(arg)) {
        if (callee.kind === ts.SyntaxKind.ImportKeyword) out.push(arg.text);
        else if (ts.isIdentifier(callee) && callee.text === "require") out.push(arg.text);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

const sources = new Set();
const edges = new Set();
// Specifiers the compiler resolved to an in-repo declaration file (.d.ts),
// typically a workspace package's built `types` entry. Not edges (a .d.ts is
// not a source file), but recorded so the scorer can label codemap edges
// that resolve the same import to the package's source instead.
const dtsResolutions = new Set();
const stats = { configs: configs.length, parsed_files: 0, file_config_pairs: 0, specifiers: 0, resolved_in_repo: 0, unresolved: 0, config_errors: [] };
const parsedOnce = new Set();

for (const cfgPath of configs) {
  const cfgDir = path.dirname(cfgPath);
  const read = ts.readConfigFile(cfgPath, ts.sys.readFile);
  if (read.error) { stats.config_errors.push(`${path.relative(root, cfgPath)}: ${ts.flattenDiagnosticMessageText(read.error.messageText, "\n")}`); continue; }
  let parsed;
  try {
    parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, cfgDir, undefined, cfgPath);
  } catch (e) { stats.config_errors.push(`${path.relative(root, cfgPath)}: ${e.message}`); continue; }
  for (const d of parsed.errors) {
    if (d.category === ts.DiagnosticCategory.Error) stats.config_errors.push(`${path.relative(root, cfgPath)}: ${ts.flattenDiagnosticMessageText(d.messageText, "\n")}`);
  }
  const options = parsed.options;
  const host = ts.createCompilerHost(options, true);
  const cache = ts.createModuleResolutionCache(cfgDir, (x) => host.getCanonicalFileName(x), options);
  for (const fileName of parsed.fileNames) {
    const rel = inRepo(fileName);
    if (rel === null || isDts(rel)) continue;
    if (!/\.(ts|tsx|mts|cts|js|jsx|mjs|cjs)$/.test(rel)) continue;
    sources.add(rel);
    let text;
    try { text = fs.readFileSync(fileName, "utf8"); } catch { continue; }
    const sf = ts.createSourceFile(fileName, text, options.target ?? ts.ScriptTarget.Latest, true);
    stats.file_config_pairs++;
    if (!parsedOnce.has(rel)) { parsedOnce.add(rel); stats.parsed_files++; }
    for (const spec of specifiers(sf)) {
      stats.specifiers++;
      const r = ts.resolveModuleName(spec, fileName, options, host, cache);
      const rm = r.resolvedModule;
      if (!rm) { stats.unresolved++; continue; }
      const dst = inRepo(rm.resolvedFileName);
      if (dst !== null && isDts(dst)) { dtsResolutions.add(`${rel}\t${dst}`); continue; }
      if (dst === null || dst === rel) continue;
      edges.add(`${rel}\t${dst}`);
      stats.resolved_in_repo++;
    }
  }
}

fs.writeFileSync(outPath, [...edges].sort().map((e) => e + "\n").join(""));
fs.writeFileSync(outPath + ".files", [...sources].sort().map((f) => f + "\n").join(""));
fs.writeFileSync(outPath + ".dts", [...dtsResolutions].sort().map((e) => e + "\n").join(""));
const meta = { ...stats, source_files: sources.size, edges: edges.size, dts_resolutions: dtsResolutions.size, typescript: ts.version, node: process.version };
fs.writeFileSync(outPath + ".meta.json", JSON.stringify(meta, null, 1));
console.log(JSON.stringify(meta));
