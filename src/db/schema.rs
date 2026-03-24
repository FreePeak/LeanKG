use cozo::{Db, SqliteStorage};
use std::path::Path;

pub type CozoDb = Db<SqliteStorage>;

pub fn init_db(db_path: &Path) -> Result<CozoDb, Box<dyn std::error::Error>> {
    let db_file_path = if db_path.is_dir() {
        db_path.join("leankg.db")
    } else {
        db_path.to_path_buf()
    };

    let path_str = db_file_path.to_string_lossy().to_string();

    let db = cozo::new_cozo_sqlite(path_str)?;

    Ok(db)
}
