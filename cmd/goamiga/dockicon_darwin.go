//go:build !noebitengine

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// setDockIconPNG makes PNG data the application's Dock icon. A built
// goAmiga.app has it from its bundle too; this covers a bare binary.
static void setDockIconPNG(const void *data, int length) {
	@autoreleasepool {
		NSData *d = [NSData dataWithBytes:data length:length];
		NSImage *img = [[NSImage alloc] initWithData:d];
		if (img != nil) {
			[[NSApplication sharedApplication] setApplicationIconImage:img];
		}
	}
}
*/
import "C"

import "unsafe"

func setDockIcon(png []byte) {
	C.setDockIconPNG(unsafe.Pointer(&png[0]), C.int(len(png)))
}
