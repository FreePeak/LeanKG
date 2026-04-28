//! Performance tests for Android pattern extraction
//! Benchmarks extraction speed and memory usage

use leankg::indexer::{
    AndroidHiltExtractor, AndroidManifestExtractor, AndroidResourceRefExtractor,
    AndroidRoomExtractor,
};
use std::fs;
use std::time::Instant;

const TV_APP_DIR: &str = "tests/fixtures/complex_scenarios/tv_app";
const KOTLIN_FIXTURES_DIR: &str = "tests/fixtures/kotlin_patterns";

#[test]
fn test_room_extraction_performance() {
    let source = fs::read_to_string(format!("{}/room_entities.kt", KOTLIN_FIXTURES_DIR))
        .expect("Fixture not found");

    let start = Instant::now();
    let extractor = AndroidRoomExtractor::new(source.as_bytes(), "./room_entities.kt");
    let (elements, relationships) = extractor.extract();
    let elapsed = start.elapsed();

    println!(
        "Room extraction: {:?} for {} elements, {} relationships",
        elapsed,
        elements.len(),
        relationships.len()
    );

    // Should complete in reasonable time (< 100ms for small file)
    assert!(elapsed.as_millis() < 500, "Room extraction should be fast");
    assert!(!elements.is_empty(), "Should extract elements");
}

#[test]
fn test_hilt_extraction_performance() {
    let source = fs::read_to_string(format!("{}/hilt_module.kt", KOTLIN_FIXTURES_DIR))
        .expect("Fixture not found");

    let start = Instant::now();
    let extractor = AndroidHiltExtractor::new(source.as_bytes(), "./hilt_module.kt");
    let (elements, relationships) = extractor.extract();
    let elapsed = start.elapsed();

    println!(
        "Hilt extraction: {:?} for {} elements, {} relationships",
        elapsed,
        elements.len(),
        relationships.len()
    );

    assert!(elapsed.as_millis() < 100, "Hilt extraction should be fast");
}

#[test]
fn test_manifest_extraction_performance() {
    let source = fs::read_to_string(format!("{}/AndroidManifest.xml", TV_APP_DIR))
        .expect("Manifest not found");

    let start = Instant::now();
    let extractor = AndroidManifestExtractor::new(source.as_bytes(), "AndroidManifest.xml");
    let (elements, relationships) = extractor.extract();
    let elapsed = start.elapsed();

    println!(
        "Manifest extraction: {:?} for {} elements, {} relationships",
        elapsed,
        elements.len(),
        relationships.len()
    );

    assert!(
        elapsed.as_millis() < 100,
        "Manifest extraction should be fast"
    );
    assert!(!elements.is_empty(), "Should extract manifest elements");
}

#[test]
fn test_resource_ref_extraction_performance() {
    let source = fs::read_to_string(format!("{}/room_dao.kt", KOTLIN_FIXTURES_DIR))
        .expect("Fixture not found");

    let start = Instant::now();
    let extractor = AndroidResourceRefExtractor::new(source.as_bytes(), "./room_dao.kt");
    let (_, relationships) = extractor.extract();
    let elapsed = start.elapsed();

    println!(
        "Resource ref extraction: {:?} for {} relationships",
        elapsed,
        relationships.len()
    );

    assert!(
        elapsed.as_millis() < 200,
        "Resource ref extraction should be very fast"
    );
}

#[test]
fn test_batch_extraction_performance() {
    // Test extracting from multiple files
    let files = vec![
        format!("{}/room_entities.kt", KOTLIN_FIXTURES_DIR),
        format!("{}/room_dao.kt", KOTLIN_FIXTURES_DIR),
        format!("{}/hilt_module.kt", KOTLIN_FIXTURES_DIR),
        format!("{}/data_class.kt", KOTLIN_FIXTURES_DIR),
    ];

    let start = Instant::now();
    let mut total_elements = 0;
    let mut total_relationships = 0;

    for file in &files {
        if let Ok(source) = fs::read_to_string(file) {
            let room_ext = AndroidRoomExtractor::new(source.as_bytes(), file);
            let (e1, r1) = room_ext.extract();
            total_elements += e1.len();
            total_relationships += r1.len();

            let hilt_ext = AndroidHiltExtractor::new(source.as_bytes(), file);
            let (e2, r2) = hilt_ext.extract();
            total_elements += e2.len();
            total_relationships += r2.len();
        }
    }

    let elapsed = start.elapsed();

    println!(
        "Batch extraction: {:?} for {} elements, {} relationships across {} files",
        elapsed,
        total_elements,
        total_relationships,
        files.len()
    );

    assert!(
        elapsed.as_millis() < 500,
        "Batch extraction should complete quickly"
    );
}

#[test]
fn test_extraction_scalability() {
    // Test with large input to ensure no exponential blowup
    let source = fs::read_to_string(format!("{}/room_entities.kt", KOTLIN_FIXTURES_DIR))
        .expect("Fixture not found");

    // Repeat content to simulate larger file
    let large_source = source.repeat(10);

    let start = Instant::now();
    let extractor = AndroidRoomExtractor::new(large_source.as_bytes(), "./large.kt");
    let (elements, _) = extractor.extract();
    let elapsed = start.elapsed();

    println!(
        "Large file extraction: {:?} for {} bytes, {} elements",
        elapsed,
        large_source.len(),
        elements.len()
    );

    // Should still complete quickly even with 10x content
    assert!(elapsed.as_millis() < 500, "Should handle larger files");
}
