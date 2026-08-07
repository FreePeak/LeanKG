#!/usr/bin/env node
/*
 * Semantic version release for LeanKG.
 *
 * Bump rules (highest present wins), Conventional Commits:
 *   BREAKING CHANGE / type!:  -> major (X)
 *   feat                      -> minor (Y)
 *   fix                       -> patch (Z)
 *   docs / chore / ci / …     -> no bump
 *
 * Release metadata commits (`release: vX.Y.Z`, `chore(main): release …`)
 * are ignored so they never re-trigger a bump.
 *
 * Modes:
 *   create-pr  — compute next version from commits since last v* tag, bump
 *                Cargo.toml / Cargo.lock / manifest.json / CHANGELOG.md on a
 *                release/vX.Y.Z branch, open/update a release PR.
 *   release    — on release-PR merge: create annotated tag, GitHub Release,
 *                and let release.yml's on.push.tags build binaries + publish
 *                crates.io.
 *
 * No external deps. Node 20 + gh CLI preinstalled on ubuntu-latest.
 */
'use strict';

const { execSync } = require('child_process');
const fs = require('fs');

const MODE = process.env.INPUT_MODE;
const REPO = process.env.GITHUB_REPOSITORY;
const HEAD_SHA = process.env.GITHUB_SHA;
const GH_TOKEN = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;

function run(cmd, opts = {}) {
  return execSync(cmd, { encoding: 'utf8', ...opts }).trim();
}
function sh(cmd, opts = {}) {
  try {
    return run(cmd, opts);
  } catch (e) {
    return '';
  }
}
function gh(args, opts = {}) {
  // Do not pass `gh --repo …` before `api` — some gh versions reject it
  // ("unknown flag: --repo"). Endpoints already include owner/repo; PR
  // commands run against the checked-out repository.
  return sh(`gh ${args}`.trim(), opts);
}

// ---------------------------------------------------------------------------
// Version helpers
// ---------------------------------------------------------------------------
function parse(v) {
  const m = String(v).match(/^(\d+)\.(\d+)\.(\d+)/);
  if (!m) throw new Error(`Not a semver: ${v}`);
  return m.slice(1, 4).map(Number);
}
function bump(v, level) {
  const [X, Y, Z] = parse(v);
  if (level === 'major') return `${X + 1}.0.0`;
  if (level === 'minor') return `${X}.${Y + 1}.0`;
  if (level === 'patch') return `${X}.${Y}.${Z + 1}`;
  throw new Error(`Unknown bump level: ${level}`);
}

// Current version: manifest.json is the source of truth; Cargo.toml is patched
// to match (release-please used the same two sources).
function currentVersion() {
  const manifest = JSON.parse(fs.readFileSync('manifest.json', 'utf8'));
  const v = manifest['.'];
  if (!v) throw new Error('manifest.json["."] missing version string');
  return v;
}

function isReleaseMetaSubject(s) {
  // Ignore version-bump / release-PR commits so they never recurse.
  return (
    /^release:/i.test(s) ||
    /^chore(\([^)]*\))?: release\b/i.test(s) ||
    /^chore\(main\): release\b/i.test(s)
  );
}

function isBreakingSubject(s) {
  return /^(?:[a-zA-Z]+)(?:\([^)]*\))?!:/.test(s) || /BREAKING CHANGE/i.test(s);
}

// Prefer the latest SemVer vX.Y.Z tag (ignore date-style tags like
// v2026.03.25-…). Fall back to git-describe if none exist.
function lastSemverTag() {
  const tags = sh(`git tag -l 'v*' --sort=-v:refname`)
    .split('\n')
    .map((t) => t.trim())
    .filter(Boolean);
  const semver = tags.find((t) => /^v\d+\.\d+\.\d+$/.test(t));
  if (semver) return semver;
  return sh(`git describe --tags --abbrev=0 --match 'v*' 2>/dev/null`);
}

// Commits to consider: those reachable from HEAD but not from the last v* tag.
function bumpWorthyCommits() {
  const lastTag = lastSemverTag();
  const range = lastTag ? `${lastTag}..HEAD` : '';
  const subjects = range
    ? run(`git log --format=%s ${range}`).split('\n')
    : run('git log --format=%s').split('\n');
  const out = [];
  for (const s of subjects) {
    if (!s || isReleaseMetaSubject(s)) continue;
    const m = s.match(/^([a-zA-Z]+)(?:\([^)]*\))?(!)?:/);
    if (!m) continue;
    const type = m[1];
    const breaking = !!m[2] || isBreakingSubject(s);
    // Conventional: only feat/fix (and explicit breaking) move the version.
    if (!breaking && !['feat', 'fix'].includes(type)) continue;
    out.push({ subject: s, type, breaking });
  }
  return out;
}

function classify(commits) {
  if (commits.some((c) => c.breaking)) return 'major';
  if (commits.some((c) => c.type === 'feat')) return 'minor';
  if (commits.some((c) => c.type === 'fix')) return 'patch';
  return null;
}

function changelogSection(v, prevV) {
  const date = new Date().toISOString().slice(0, 10);
  return `## [${v}](https://github.com/${REPO}/compare/v${prevV}...v${v}) (${date})`;
}

// ---------------------------------------------------------------------------
// create-pr mode
// ---------------------------------------------------------------------------
function createPr() {
  const commits = bumpWorthyCommits();
  const level = classify(commits);
  if (!level) {
    console.log('No feat/fix/breaking commits since last tag — no release needed.');
    process.exit(0);
  }
  const prev = currentVersion();
  const next = bump(prev, level);
  console.log(
    `commits: ${commits.map((c) => (c.breaking ? `${c.type}!` : c.type)).join(', ')} -> ${level}: ${prev} -> ${next}`,
  );

  // Refresh a release branch if one already exists for `next` (idempotent:
  // subsequent pushes to main update the open PR instead of opening a new one).
  const branch = `release/v${next}`;
  const existing = gh(`api repos/${REPO}/git/ref/heads/${branch} --jq .ref`);
  if (existing) {
    run(`git fetch origin ${branch}`);
    run(`git checkout -B ${branch} origin/${branch}`);
  } else {
    run(`git checkout -b ${branch}`);
  }

  // Bump version metadata.
  bumpCargoToml(next);
  bumpCargoLock(next);
  bumpManifest(next);
  prependChangelog(next, prev);

  run(`git add Cargo.toml Cargo.lock manifest.json CHANGELOG.md`);
  run(`git -c user.name='leankg-release[bot]' -c user.email='noreply@github.com' commit -m 'release: v${next}'`);

  run(`git push origin ${branch}`);
  const pr = gh(`pr list --head ${branch} --json number,url --jq '.[0].url'`);
  if (pr) {
    console.log(`Updated release PR: ${pr}`);
  } else {
    gh(
      `pr create --base main --head ${branch} --title 'release: v${next}' --body 'Semantic release v${next} (${level} bump).'`,
    );
    console.log(`Opened release PR for ${branch}`);
  }
}

function bumpCargoToml(v) {
  const f = 'Cargo.toml';
  const src = fs.readFileSync(f, 'utf8');
  const next = src.replace(/^version = "\d+\.\d+\.\d+"/m, `version = "${v}"`);
  if (next === src) throw new Error(`No version line found in ${f}`);
  fs.writeFileSync(f, next);
}
function bumpCargoLock(v) {
  // Match only the root package block (first "name = "leankg"" occurrence).
  const f = 'Cargo.lock';
  const src = fs.readFileSync(f, 'utf8');
  const i = src.indexOf('name = "leankg"');
  if (i === -1) return;
  const j = src.indexOf('version = "', i);
  if (j === -1 || j > i + 200) return;
  const k = src.indexOf('"', j + 11);
  fs.writeFileSync(f, src.slice(0, j + 11) + v + src.slice(k));
}
function bumpManifest(v) {
  const f = 'manifest.json';
  const m = JSON.parse(fs.readFileSync(f, 'utf8'));
  m['.'] = v;
  fs.writeFileSync(f, JSON.stringify(m, null, 2) + '\n');
}
function prependChangelog(v, prev) {
  const f = 'CHANGELOG.md';
  const src = fs.readFileSync(f, 'utf8');
  const section = changelogSection(v, prev);
  // Insert after the "# Changelog" header.
  const idx = src.indexOf('\n');
  fs.writeFileSync(f, src.slice(0, idx + 1) + '\n' + section + '\n\n' + src.slice(idx + 1));
}

// ---------------------------------------------------------------------------
// release mode
// ---------------------------------------------------------------------------
function release() {
  // The checkout is at the merged release branch; Cargo.toml already has the
  // new version. Tag + GitHub Release. The pushed tag natively fires
  // release.yml's on.push.tags for binaries + crates.io.
  const v = currentVersion();
  const tag = `v${v}`;
  console.log(`Releasing ${tag}`);

  const tagExists = sh(`git ls-remote --tags origin ${tag}`);
  if (tagExists) {
    console.log(`Tag ${tag} already on origin — skipping tag push.`);
  } else {
    run(`git -c user.name='leankg-release[bot]' -c user.email='noreply@github.com' tag -a ${tag} -m 'Release ${tag}'`);
    run(`git push origin ${tag}`);
    console.log(`Pushed tag ${tag}`);
  }

  const rel = gh(`release view ${tag} --json url --jq .url`);
  if (rel) {
    console.log(`Release ${tag} already exists: ${rel}`);
  } else {
    gh(`release create ${tag} --target ${HEAD_SHA} --generate-notes`);
    console.log(`Created GitHub Release ${tag}`);
  }
}

// ---------------------------------------------------------------------------
if (MODE === 'create-pr') createPr();
else if (MODE === 'release') release();
else {
  console.error(`Unknown mode: ${MODE}`);
  process.exit(1);
}
