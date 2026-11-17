//! Command-line entry of rsastdump: `rsastdump <file.rs> [<file.rs> ...]`
//! prints `{"files": [...]}` (see the library docs) on stdout.

use std::io::Write;
use std::process::ExitCode;

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.is_empty() {
        eprintln!("usage: rsastdump <file.rs> [<file.rs> ...]");
        return ExitCode::from(2);
    }
    let out = rsastdump::dump_all(&args);
    let mut stdout = std::io::stdout().lock();
    let written = serde_json::to_writer(&mut stdout, &out)
        .map_err(std::io::Error::from)
        .and_then(|()| writeln!(stdout));
    match written {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("rsastdump: {e}");
            ExitCode::FAILURE
        }
    }
}
