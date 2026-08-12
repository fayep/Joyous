// Overrides lodepng's own malloc/realloc/free (LODEPNG_NO_COMPILE_ALLOCATORS
// in platformio.ini) to route its buffers -- a full ~2MB PNG file plus a
// ~960KB decoded 4bpp buffer -- through PSRAM instead of the ~300KB
// internal SRAM heap.
//
// This has to live inside the lodepng library itself (this directory),
// not in our own src/*.cpp files, because of how GNU ld resolves symbols
// from static libraries: it scans each .a archive once, pulling in only
// the object files needed to resolve symbols already outstanding at that
// point. If our own object files (which happen to come later in the link
// order) are what provide lodepng_malloc/realloc/free, the linker has
// already finished with liblodepng.a by the time it would need them,
// and every lodepng_malloc/realloc/free call inside lodepng.cpp.o is left
// as an unresolved symbol -- confirmed the hard way (2026-08-11). Putting
// the overrides in the same archive as the code that calls them sidesteps
// the ordering problem entirely.
//
// Also: these must NOT be `extern "C"`. lodepng.cpp's own fallback
// prototypes (its #else LODEPNG_COMPILE_ALLOCATORS branch, used when
// LODEPNG_NO_COMPILE_ALLOCATORS is set) declare these with plain C++
// linkage, so a mismatched extern "C" here produces a different mangled
// symbol that never resolves the reference -- also confirmed the hard way,
// same session.
#include "lodepng_psram_allocator.h"
#include <esp_heap_caps.h>

void *lodepng_malloc(size_t size) {
    return heap_caps_malloc(size, MALLOC_CAP_SPIRAM);
}
void *lodepng_realloc(void *ptr, size_t new_size) {
    return heap_caps_realloc(ptr, new_size, MALLOC_CAP_SPIRAM);
}
void lodepng_free(void *ptr) {
    heap_caps_free(ptr);
}
