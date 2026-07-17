//! Differential SIMD vs scalar (FR-VE-TEST-SIMD). Abs error must be < 1e-6.
//! For INT8 dots the paths must match exactly.

#[cfg(test)]
mod tests {
    use crate::vector_engine::{detect_simd, dot_i8, SimdKind};

    #[test]
    fn neon_avx_scalar_agree_exactly() {
        let a: Vec<i8> = (0..128).map(|i| ((i * 3) % 17) as i8 - 8).collect();
        let b: Vec<i8> = (0..128).map(|i| ((i * 5) % 13) as i8 - 6).collect();
        let scalar = dot_i8(&a, &b, SimdKind::Scalar) as f64;
        for kind in [
            SimdKind::Neon,
            SimdKind::Avx2,
            SimdKind::Avx512,
            detect_simd(),
        ] {
            let got = dot_i8(&a, &b, kind) as f64;
            assert!(
                (got - scalar).abs() < 1e-6,
                "kind={:?} got={got} scalar={scalar}",
                kind
            );
        }
    }
}
