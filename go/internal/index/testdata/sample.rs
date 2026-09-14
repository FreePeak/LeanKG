pub struct Config {
    pub debug: bool,
}

pub fn parse(input: &str) -> Config {
    let cfg = Config { debug: false };
    normalize(cfg)
}

fn normalize(c: Config) -> Config {
    c
}
