package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	rom, err := os.ReadFile("ROMS/Kick13.rom")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// ROM is mapped at $FC0000. CPU stopped at $FC0D14.
	// Let's look at the code around that address.
	offset := 0x0D14
	fmt.Printf("=== ROM disassembly around $FC%04X (offset $%04X) ===\n", offset, offset)
	for i := offset - 20; i < offset+40 && i < len(rom)-1; i += 2 {
		w := uint16(rom[i])<<8 | uint16(rom[i+1])
		fmt.Printf("  $FC%04X: $%04X\n", i, w)
	}

	// Search ROM for writes to COLOR00 ($DFF180) with red-ish values
	// Pattern: MOVE.W #$xxxx,$DFF180  = 33FC xxxx 00DF F180
	fmt.Printf("\n=== Searching for MOVE.W #imm, $DFF180 (COLOR00) ===\n")
	for i := 0; i < len(rom)-8; i += 2 {
		// MOVE.W #imm,abs.L = $33FC imm16 addr32
		if rom[i] == 0x33 && rom[i+1] == 0xFC {
			imm := uint16(rom[i+2])<<8 | uint16(rom[i+3])
			addr := uint32(rom[i+4])<<24 | uint32(rom[i+5])<<16 | uint32(rom[i+6])<<8 | uint32(rom[i+7])
			if addr == 0x00DFF180 {
				r := (imm >> 8) & 0xF
				g := (imm >> 4) & 0xF
				b := imm & 0xF

				fmt.Printf("  ROM+$%05X ($FC%04X): MOVE.W #$%04X,$DFF180 (COLOR00) R=%d G=%d B=%d\n",
					i, i, imm, r, g, b)
			}
		}
	}

	// Search for the Alert function
	// Alert typically starts with: MOVEM.L regs,-(SP) then does various setup
	// Look for the color register writes that make the red border
	fmt.Printf("\n=== Searching for MOVE.W #$0xxx,$DFF180 (any direct COLOR00 write) ===\n")
	for i := 0; i < len(rom)-6; i += 2 {
		// MOVE.W #imm,(An) = 317C imm
		// MOVE.W #imm,$xxx(An) = 317C imm disp
		// More commonly: MOVE.W #imm,$180(A5) where A5=$DFF000
		if rom[i] == 0x3B && rom[i+1] == 0x7C { // MOVE.W #imm,d(A5)
			imm := uint16(rom[i+2])<<8 | uint16(rom[i+3])
			disp := int16(uint16(rom[i+4])<<8 | uint16(rom[i+5]))
			if disp == 0x180 { // COLOR00
				r := (imm >> 8) & 0xF
				g := (imm >> 4) & 0xF
				b := imm & 0xF
				fmt.Printf("  ROM+$%05X ($FC%04X): MOVE.W #$%04X,$180(A5) R=%d G=%d B=%d\n",
					i, i, imm, r, g, b)
			}
		}
	}

	// Also search for the MOVE.W Dn,$180(A5) pattern which toggles colors
	fmt.Printf("\n=== Searching for MOVE.W Dn,$180(A5) (dynamic COLOR00 write) ===\n")
	for i := 0; i < len(rom)-4; i += 2 {
		w := uint16(rom[i])<<8 | uint16(rom[i+1])
		// MOVE.W Dn,d(A5) = 3B4n disp where n=0-7
		if w&0xFFF8 == 0x3B40 { // MOVE.W Dn,d(A5)
			disp := int16(uint16(rom[i+2])<<8 | uint16(rom[i+3]))
			if disp == 0x180 {
				dn := w & 0x7
				fmt.Printf("  ROM+$%05X ($FC%04X): MOVE.W D%d,$180(A5)\n", i, i, dn)
			}
		}
	}

	// Look for the Alert text strings
	fmt.Printf("\n=== Searching for 'Software Failure' in ROM ===\n")
	needle := []byte("Software Failure")
	for i := 0; i < len(rom)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if rom[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			fmt.Printf("  Found at ROM+$%05X ($FC%04X)\n", i, i)
			// Print surrounding text
			start := i - 16
			if start < 0 {
				start = 0
			}
			end := i + 64
			if end > len(rom) {
				end = len(rom)
			}
			fmt.Printf("  Context: ")
			for j := start; j < end; j++ {
				if rom[j] >= 0x20 && rom[j] < 0x7F {
					fmt.Printf("%c", rom[j])
				} else {
					fmt.Printf(".")
				}
			}
			fmt.Println()
		}
	}

	// Look for the "Guru Meditation" text
	fmt.Printf("\n=== Searching for 'Guru' in ROM ===\n")
	needle2 := []byte("Guru")
	for i := 0; i < len(rom)-len(needle2); i++ {
		match := true
		for j := 0; j < len(needle2); j++ {
			if rom[i+j] != needle2[j] {
				match = false
				break
			}
		}
		if match {
			fmt.Printf("  Found at ROM+$%05X ($FC%04X)\n", i, i)
			end := i + 64
			if end > len(rom) {
				end = len(rom)
			}
			fmt.Printf("  Text: ")
			for j := i; j < end; j++ {
				if rom[j] >= 0x20 && rom[j] < 0x7F {
					fmt.Printf("%c", rom[j])
				} else if rom[j] == 0 {
					break
				} else {
					fmt.Printf(".")
				}
			}
			fmt.Println()
		}
	}

	// Examine the STOP instruction location ($FC0D14)
	// STOP = 0x4E72 + immediate word
	fmt.Printf("\n=== Looking for STOP instructions ===\n")
	for i := 0; i < len(rom)-3; i += 2 {
		if rom[i] == 0x4E && rom[i+1] == 0x72 {
			imm := uint16(rom[i+2])<<8 | uint16(rom[i+3])
			fmt.Printf("  ROM+$%05X ($FC%04X): STOP #$%04X\n", i, i, imm)
		}
	}

	// Examine code around $FC0D14
	fmt.Printf("\n=== Code around $FC0D14 ===\n")
	base := 0x0D00
	for i := base; i < base+60 && i+1 < len(rom); i += 2 {
		w := uint16(rom[i])<<8 | uint16(rom[i+1])
		fmt.Printf("  $FC%04X: %04X", i, w)
		// crude disassembly for common ops
		switch {
		case w == 0x4E72:
			imm := uint16(rom[i+2])<<8 | uint16(rom[i+3])
			fmt.Printf("  STOP #$%04X", imm)
		case w == 0x4E73:
			fmt.Printf("  RTE")
		case w == 0x4E75:
			fmt.Printf("  RTS")
		}
		fmt.Println()
	}

	// Check what the exception vectors resolve to in ROM
	// Initial SSP is at $000000-$000003, PC at $000004-$000007
	fmt.Printf("\n=== ROM Reset Vectors ===\n")
	ssp := binary.BigEndian.Uint32(rom[0:4])
	pc := binary.BigEndian.Uint32(rom[4:8])
	fmt.Printf("  Initial SSP: $%08X\n", ssp)
	fmt.Printf("  Initial PC:  $%08X\n", pc)
}
