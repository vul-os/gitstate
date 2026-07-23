/* Portability shim for macOS.
 *
 * The vendored libgit2 (libgit2-sys, apple target) is built with
 * `GIT_USE_ICONV` and, via the SDK's `iconv.h` macros, imports the GNU-style
 * `libiconv_*` symbols. On recent macOS the runtime /usr/lib/libiconv.2.dylib
 * exports only the Apple `iconv_*` names, so `libiconv_open` resolves to NULL
 * and the first ref-name normalization (e.g. `Repository::head`) jumps through
 * a null pointer and crashes.
 *
 * This translation unit provides the missing GNU names, forwarding to the
 * Apple `iconv_*` functions resolved at runtime with `dlsym(RTLD_DEFAULT, …)`.
 * Using dlsym keeps the shim free of any link-time dependency on a specific
 * exported symbol name, so it links cleanly into every dependent binary
 * (daemon, CLI, tests) without extra linker flags. No-op on non-Apple targets
 * (this file is only compiled there — see build.rs).
 */
#include <stddef.h>
#include <dlfcn.h>

typedef void *iconv_t;
typedef iconv_t (*open_fn)(const char *, const char *);
typedef size_t (*conv_fn)(iconv_t, char **, size_t *, char **, size_t *);
typedef int (*close_fn)(iconv_t);

iconv_t libiconv_open(const char *to, const char *from) {
    static open_fn f;
    if (!f) f = (open_fn)dlsym(RTLD_DEFAULT, "iconv_open");
    return f ? f(to, from) : (iconv_t)-1;
}

size_t libiconv(iconv_t cd, char **in, size_t *inl, char **out, size_t *outl) {
    static conv_fn f;
    if (!f) f = (conv_fn)dlsym(RTLD_DEFAULT, "iconv");
    return f ? f(cd, in, inl, out, outl) : (size_t)-1;
}

int libiconv_close(iconv_t cd) {
    static close_fn f;
    if (!f) f = (close_fn)dlsym(RTLD_DEFAULT, "iconv_close");
    return f ? f(cd) : 0;
}
