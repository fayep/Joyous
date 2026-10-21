#pragma once
// Prototypes for lodepng_psram_allocator.cpp's overrides. lodepng.h only
// declares these when LODEPNG_COMPILE_ALLOCATORS is set (i.e. when using
// lodepng's own malloc/free) -- with LODEPNG_NO_COMPILE_ALLOCATORS (see
// platformio.ini), callers of lodepng_free/etc. need their own declaration.
//
// Deliberately NOT extern "C": lodepng.cpp's own fallback prototypes (its
// #else LODEPNG_COMPILE_ALLOCATORS branch) declare these with plain C++
// linkage, so ours must match exactly or the linker sees two different
// (mangled vs unmangled) symbols and neither resolves the other.
#include <cstddef>

void *lodepng_malloc(size_t size);
void *lodepng_realloc(void *ptr, size_t new_size);
void lodepng_free(void *ptr);
