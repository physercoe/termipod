use std::path::{Path, PathBuf};

// workspacefs.rs — filesystem mutations for the Author (J2) file tree: create,
// rename, delete, move, copy. Every command operates on absolute paths that come
// from `workspace_list` (the user's own picked folder). Names for create/rename
// are validated to be a single path segment, so they can never traverse out of
// the parent directory; move/copy refuse to descend a folder into itself.

fn bare_name(name: &str) -> Result<String, String> {
    let n = name.trim();
    if n.is_empty() {
        return Err("empty name".into());
    }
    if n == "." || n == ".." || n.contains('/') || n.contains('\\') {
        return Err("name must be a single path segment".into());
    }
    Ok(n.to_string())
}

fn basename(path: &Path) -> Result<String, String> {
    path.file_name()
        .map(|s| s.to_string_lossy().to_string())
        .ok_or_else(|| "path has no file name".into())
}

// Whether `child` is `ancestor` or lives underneath it — used to reject moving or
// copying a folder into itself (which would recurse forever / lose data). Prefers
// canonicalized paths; falls back to a literal prefix test if either can't resolve.
fn is_within(child: &Path, ancestor: &Path) -> bool {
    match (child.canonicalize(), ancestor.canonicalize()) {
        (Ok(c), Ok(a)) => c.starts_with(&a),
        _ => child.starts_with(ancestor),
    }
}

fn copy_recursive(src: &Path, dst: &Path) -> Result<(), String> {
    if src.is_dir() {
        std::fs::create_dir_all(dst).map_err(|e| e.to_string())?;
        for ent in std::fs::read_dir(src).map_err(|e| e.to_string())?.flatten() {
            copy_recursive(&ent.path(), &dst.join(ent.file_name()))?;
        }
        Ok(())
    } else {
        std::fs::copy(src, dst).map(|_| ()).map_err(|e| e.to_string())
    }
}

/// Create an empty file `dir/name`. Errors if it already exists.
#[tauri::command]
pub async fn workspace_new_file(dir: String, name: String) -> Result<String, String> {
    let name = bare_name(&name)?;
    let base = PathBuf::from(&dir);
    if !base.is_dir() {
        return Err(format!("not a folder: {dir}"));
    }
    let target = base.join(&name);
    if target.exists() {
        return Err(format!("already exists: {}", target.display()));
    }
    std::fs::write(&target, "").map_err(|e| e.to_string())?;
    Ok(target.to_string_lossy().to_string())
}

/// Create a new folder `dir/name`. Errors if it already exists.
#[tauri::command]
pub async fn workspace_new_folder(dir: String, name: String) -> Result<String, String> {
    let name = bare_name(&name)?;
    let base = PathBuf::from(&dir);
    if !base.is_dir() {
        return Err(format!("not a folder: {dir}"));
    }
    let target = base.join(&name);
    if target.exists() {
        return Err(format!("already exists: {}", target.display()));
    }
    std::fs::create_dir(&target).map_err(|e| e.to_string())?;
    Ok(target.to_string_lossy().to_string())
}

/// Rename `path` to a sibling `name` (a bare filename). Returns the new path.
#[tauri::command]
pub async fn workspace_rename(path: String, name: String) -> Result<String, String> {
    let name = bare_name(&name)?;
    let src = PathBuf::from(&path);
    if !src.exists() {
        return Err(format!("not found: {path}"));
    }
    let parent = src.parent().ok_or("path has no parent")?;
    let target = parent.join(&name);
    if target == src {
        return Ok(path);
    }
    if target.exists() {
        return Err(format!("already exists: {}", target.display()));
    }
    std::fs::rename(&src, &target).map_err(|e| e.to_string())?;
    Ok(target.to_string_lossy().to_string())
}

/// Delete a file, or a directory (recursively).
#[tauri::command]
pub async fn workspace_delete(path: String) -> Result<(), String> {
    let p = PathBuf::from(&path);
    if !p.exists() {
        return Err(format!("not found: {path}"));
    }
    if p.is_dir() {
        std::fs::remove_dir_all(&p).map_err(|e| e.to_string())
    } else {
        std::fs::remove_file(&p).map_err(|e| e.to_string())
    }
}

/// Move `src` into `dest_dir` (keeping its basename). Returns the new path.
#[tauri::command]
pub async fn workspace_move(src: String, dest_dir: String) -> Result<String, String> {
    let from = PathBuf::from(&src);
    let dest = PathBuf::from(&dest_dir);
    if !from.exists() {
        return Err(format!("not found: {src}"));
    }
    if !dest.is_dir() {
        return Err(format!("not a folder: {dest_dir}"));
    }
    if from.is_dir() && is_within(&dest, &from) {
        return Err("cannot move a folder into itself".into());
    }
    let name = basename(&from)?;
    let target = dest.join(&name);
    if target == from {
        return Ok(src);
    }
    if target.exists() {
        return Err(format!("already exists: {}", target.display()));
    }
    // `rename` works within one filesystem; fall back to copy+delete across devices.
    if std::fs::rename(&from, &target).is_err() {
        copy_recursive(&from, &target)?;
        if from.is_dir() {
            std::fs::remove_dir_all(&from).map_err(|e| e.to_string())?;
        } else {
            std::fs::remove_file(&from).map_err(|e| e.to_string())?;
        }
    }
    Ok(target.to_string_lossy().to_string())
}

/// Copy `src` into `dest_dir` (recursively for a directory). Returns the new path.
#[tauri::command]
pub async fn workspace_copy(src: String, dest_dir: String) -> Result<String, String> {
    let from = PathBuf::from(&src);
    let dest = PathBuf::from(&dest_dir);
    if !from.exists() {
        return Err(format!("not found: {src}"));
    }
    if !dest.is_dir() {
        return Err(format!("not a folder: {dest_dir}"));
    }
    if from.is_dir() && is_within(&dest, &from) {
        return Err("cannot copy a folder into itself".into());
    }
    let name = basename(&from)?;
    let target = dest.join(&name);
    if target.exists() {
        return Err(format!("already exists: {}", target.display()));
    }
    copy_recursive(&from, &target)?;
    Ok(target.to_string_lossy().to_string())
}
