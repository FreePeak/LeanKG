use rust_embed::Embed;
use std::borrow::Cow;

#[derive(Embed)]
#[folder = "src/embed/"]
struct Assets;

static NOT_FOUND: &[u8] = b"<html><body><h1>404 Not Found</h1></body></html>";

pub fn get(file_path: &str) -> Option<Cow<'static, [u8]>> {
    Assets::get(file_path).map(|f| Cow::Owned(f.data.into_owned()))
}

pub fn get_404() -> &'static [u8] {
    NOT_FOUND
}
