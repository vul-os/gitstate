// On Apple targets, link a tiny iconv-name shim next to the vendored libgit2.
// See iconv_shim.c for the full rationale. Elsewhere this is a no-op.
fn main() {
    let target = std::env::var("TARGET").unwrap_or_default();
    if target.contains("apple") {
        cc::Build::new()
            .file("iconv_shim.c")
            .compile("gitstate_iconv_shim");
        println!("cargo:rerun-if-changed=iconv_shim.c");
    }
}
