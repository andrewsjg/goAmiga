//go:build !noebitengine

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>

static long pasteboardChangeCount(void) {
	return (long)[[NSPasteboard generalPasteboard] changeCount];
}

// pasteboardText returns the pasteboard's text as UTF-8 (malloc'd; the
// caller frees it), or NULL if it holds none.
static char *pasteboardText(void) {
	@autoreleasepool {
		NSString *s = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
		if (s == nil) {
			return NULL;
		}
		return strdup([s UTF8String]);
	}
}

static void setPasteboardText(const char *text) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		[pb setString:[NSString stringWithUTF8String:text] forType:NSPasteboardTypeString];
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/andrewsjg/goamiga/amiga"
)

// macClipboard is the Mac's general pasteboard.
type macClipboard struct{}

func hostClipboard() amiga.HostClipboard { return macClipboard{} }

func (macClipboard) ChangeCount() int { return int(C.pasteboardChangeCount()) }

func (macClipboard) Text() (string, bool) {
	p := C.pasteboardText()
	if p == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), true
}

func (macClipboard) SetText(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.setPasteboardText(cs)
}
