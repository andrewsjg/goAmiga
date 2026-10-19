//go:build !noebitengine

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// setDockIconPNG makes PNG data the application's Dock icon. A built
// goAmiga.app has it from its bundle too; this covers a bare binary. It
// runs on the main thread, as AppKit wants, whenever that next gets to
// it, and only once the windowing library has set up the application.
static void setDockIconPNG(const void *data, int length) {
	NSData *d = [NSData dataWithBytes:data length:length];
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			NSImage *img = [[NSImage alloc] initWithData:d];
			if (img != nil && NSApp != nil) {
				[NSApp setApplicationIconImage:img];
			}
		}
	});
}
*/
import "C"

import "unsafe"

func setDockIcon(png []byte) {
	C.setDockIconPNG(unsafe.Pointer(&png[0]), C.int(len(png)))
}
