---
name: leankg-obsidian-integration
overview: Integrate LeanKG with Obsidian as the annotation IDE. LeanKG generates Obsidian notes from CozoDB (push), file watcher syncs annotation edits back (pull). LeanKG remains source of truth.
todos:
  - id: design-obsidian-integration
    content: "Write spec: Obsidian integration design document"
    status: pending
  - id: implement-note-generator
    content: "Implement note generator: CozoDB → Obsidian markdown"
    status: pending
  - id: implement-sync-push
    content: "Implement leankg obsidian push command"
    status: pending
  - id: implement-sync-pull
    content: "Implement leankg obsidian pull command"
    status: pending
  - id: implement-file-watcher
    content: "Implement leankg obsidian watch (file watcher)"
    status: pending
  - id: implement-vault-init
    content: "Implement leankg obsidian init command"
    status: pending
  - id: doc-clean-room
    content: "Write clean-room rationale and run instructions"
    status: pending
isProject: false
---

# LeanKG-Obsidian Integration Spec

## Goal
Use Obsidian as the annotation IDE for LeanKG. LeanKG generates notes from CozoDB, edits in Obsidian sync back to LeanKG. LeanKG is source of truth.

## Confirmed Constraints
- **Source of truth**: LeanKG (CozoDB) - Obsidian notes are derived/generated
- **Sync model**: Hybrid - on-demand for push/pull, file watcher for ongoing edits
- **Vault location**: `.leankg/obsidian/vault/` (managed by LeanKG)
- **Your notes**: Live outside `.leankg/obsidian/vault/` - untouched

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  .leankg/obsidian/vault/  (LeanKG-managed)               │
│  ├── src/main.rs.md        (code element note)          │
│  ├── src/lib.rs.md                                       │
│  └── FEAT-auth.md          (requirement note)            │
└─────────────────────────────────────────────────────────┘
         ↑                    ↓
┌─────────────────────────────────────────────────────────┐
│  LeanKG (CozoDB)                                        │
│  ├── CodeElements                                       │
│  ├── Relationships                                      │
│  └── Annotations (BusinessLogic)                         │
└─────────────────────────────────────────────────────────┘
```

## Note Format

Each code element becomes a markdown note:

```markdown
---
leankg_id: src/main.rs::main
leankg_type: function
leankg_file: src/main.rs
leankg_line: 1-25
leankg_relationships:
  - src/lib.rs::init (calls)
  - src/config.rs::Config (imports)
leankg_annotation: ""
created: 2026-04-10T10:00:00Z
updated: 2026-04-10T10:00:00Z
---

# main

```rust
fn main() {
    // ...
}
```
```

**Annotated elements** - annotation description fills `leankg_annotation`:

```markdown
leankg_annotation: "Entry point for the application. Handles CLI arg parsing and starts the server."
```

## Sync Commands

| Command | Direction | What it does |
|---------|-----------|--------------|
| `leankg obsidian push` | LeanKG → Obsidian | Regenerates all notes from CozoDB |
| `leankg obsidian pull` | Obsidian → LeanKG | Imports annotation edits to CozoDB |
| `leankg obsidian watch` | Both (auto) | File watcher, auto-pulls on edit |
| `leankg obsidian init` | - | Initialize vault structure |
| `leankg obsidian status` | - | Show sync status |

## Conflict Handling
- `push` **overwrites** `leankg_*` frontmatter from CozoDB
- `pull` only imports `leankg_annotation` field to LeanKG
- Custom笔记 in note body are **never** overwritten
- Conflict on `leankg_annotation` → prompts for manual merge

## CLI Structure
```
leankg obsidian init [--vault ~/.obsidian/vaults/leankg]
leankg obsidian push
leankg obsidian pull
leankg obsidian watch
leankg obsidian status
```

## Implementation Phases

### Phase 1: Core Sync
- `leankg obsidian init` - create vault structure
- `leankg obsidian push` - generate notes from CozoDB
- `leankg obsidian pull` - parse annotations back to CozoDB

### Phase 2: Watch Mode
- `leankg obsidian watch` - file watcher with debounced pull

### Phase 3: Obsidian Integration
- Generate notes compatible with Obsidian's native graph view
- Support Obsidian's backlinks for requirement traceability
- Community plugin compatibility

## Risk Controls
- **Data loss**: Pull never overwrites custom note content
- **Source of truth**: Push overwrites only `leankg_*` frontmatter
- **Conflict safety**: Manual merge prompt when both sides changed

## Previous Plan Superseded
- `gitnexus-ui-migration` plan is superseded by this Obsidian integration
- The graph UI goal is now served by Obsidian's native graph view + LeanKG note generation
