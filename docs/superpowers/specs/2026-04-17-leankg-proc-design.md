# LeanKG Proc Command Design

## Overview
Add process management subcommands `leankg proc status` and `leankg proc kill` to manage LeanKG and Vite processes.

## Command Structure

### `leankg proc status`
- Lists all running `leankg` and `vite` processes
- Uses `sysinfo` crate for cross-platform process enumeration
- Output format:
  ```
  LeanKG Processes:
  ==================
  PID: 12345 | CPU: 5.2% | MEM: 1.2% | RSS: 45MB | Command: leankg serve
  PID: 12346 | CPU: 0.8% | MEM: 0.5% | RSS: 22MB | Command: vite --port 5173
  ```
- If no processes found: "No leankg or vite processes running"

### `leankg proc kill`
- Kills all `leankg` and `vite` processes using `pkill`
- Uses `std::process::Command` to call `pkill -9 -f "leankg"` and `pkill -9 -f "vite"`
- Output: "Killed all leankg and vite processes" or error message if pkill unavailable

## Implementation

### Files to Modify
- `src/cli/mod.rs` - Add `Proc` variant to `CLICommand` with subcommands
- `src/main.rs` - Add handler for `CLICommand::Proc`

### Dependencies
- Add `sysinfo` crate to `Cargo.toml` for cross-platform process status

### Subcommand Enum
```rust
Proc {
    #[command(subcommand)]
    command: ProcCommand,
}

enum ProcCommand {
    /// Show running LeanKG and Vite processes
    Status,
    /// Kill all LeanKG and Vite processes
    Kill,
}
```

## Cross-Platform Handling
- **macOS/Linux**: Both `pkill` and `sysinfo` work natively
- Fallback: If `pkill` unavailable, return error with helpful message
- `sysinfo` automatically handles differences in process info between OSes
