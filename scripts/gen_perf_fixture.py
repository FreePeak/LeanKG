#!/usr/bin/env python3
"""Generate a deterministic rust fixture for the perf gate (H8/CORE-6).

Usage: gen_perf_fixture.py <out_dir> [num_files]
Emits num_files modules with stable content (no timestamps/randomness) so
index timing is comparable across runs and machines of the same class.
"""
import sys
from pathlib import Path


def module_src(i: int) -> str:
    fns = "\n".join(
        f"    pub fn op_{j}(&self, x: u32) -> u32 {{ x.wrapping_add({i * 31 + j}) }}"
        for j in range(8)
    )
    calls = "\n".join(f"        let _ = self.op_{j}(1);" for j in range(8))
    return f"""//! Generated fixture module {i} (deterministic content — perf gate).
pub struct Module{i} {{ pub state: u32 }}

impl Module{i} {{
{fns}

    pub fn run_all(&self) {{
{calls}
    }}
}}

pub fn entry_{i}() -> Module{i} {{
    let m = Module{i} {{ state: {i} }};
    m.run_all();
    m
}}

#[cfg(test)]
mod tests {{
    use super::*;

    #[test]
    fn entry_works_{i}() {{
        assert_eq!(entry_{i}().state, {i});
    }}
}}
"""


def main() -> None:
    out = Path(sys.argv[1])
    n = int(sys.argv[2]) if len(sys.argv) > 2 else 20
    out.mkdir(parents=True, exist_ok=True)
    mods = []
    for i in range(n):
        (out / f"mod_{i:02}.rs").write_text(module_src(i))
        mods.append(f"pub mod mod_{i:02};")
    (out / "lib.rs").write_text("\n".join(mods) + "\n")
    print(f"wrote {n} modules + lib.rs to {out}")


if __name__ == "__main__":
    main()
