use leankg::embeddings::Embedder;
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let model = std::env::var("LEANKG_EMBED_MODEL").unwrap_or_else(|_| "bge".into());
    eprintln!("model_env={model}");
    let e = Embedder::new()?;
    let texts: Vec<String> = (0..32)
        .map(|i| format!("fn foo_{i}() {{ let x = {i}; x }}"))
        .collect();
    let t0 = std::time::Instant::now();
    let v = e.embed(&texts)?;
    let secs = t0.elapsed().as_secs_f64();
    println!(
        "ok n={} dim={} rate={:.1}",
        v.len(),
        v[0].len(),
        v.len() as f64 / secs.max(1e-9)
    );
    Ok(())
}
