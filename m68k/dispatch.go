package m68k

// A stepFunc is a function that executes a single processor instruction and
// returns trace information. Any errors should be returned by Processor.err.
type stepFunc func(*Processor) stepTrace

// An opcode maps an opcode to a stepFunc. Opcodes are matched by applying each
// mask and comparing the result to the base.
type opcode struct {
	base, mask uint16
	f          stepFunc
}

// Dispatch table ordered by specificity (most specific masks first within each group).
var opcodes = [][]opcode{
	{ /* 0x0... */
		{0x0800, 0xFFC0, opBtstImm}, // BTST #imm
		{0x0840, 0xFFC0, opBchgImm}, // BCHG #imm
		{0x0880, 0xFFC0, opBclrImm}, // BCLR #imm
		{0x08C0, 0xFFC0, opBsetImm}, // BSET #imm
		{0x0108, 0xF1F8, opMovep},   // MOVEP.W (d16,An),Dn
		{0x0148, 0xF1F8, opMovep},   // MOVEP.L (d16,An),Dn
		{0x0188, 0xF1F8, opMovep},   // MOVEP.W Dn,(d16,An)
		{0x01C8, 0xF1F8, opMovep},   // MOVEP.L Dn,(d16,An)
		{0x0100, 0xF1C0, opBtst},    // BTST Dn
		{0x0140, 0xF1C0, opBchg},    // BCHG Dn
		{0x0180, 0xF1C0, opBclr},    // BCLR Dn
		{0x01C0, 0xF1C0, opBset},    // BSET Dn
		{0x0000, 0xFF00, opOri},     // ORI
		{0x0200, 0xFF00, opAndi},    // ANDI
		{0x0400, 0xFF00, opSubi},    // SUBI
		{0x0600, 0xFF00, opAddi},    // ADDI
		{0x0A00, 0xFF00, opEori},    // EORI
		{0x0C00, 0xFF00, opCmpi},    // CMPI
	},
	{ /* 0x1... */
		{0x1000, 0xF000, opMove},
	},
	{ /* 0x2... */
		{0x2000, 0xF000, opMove},
	},
	{ /* 0x3... */
		{0x3000, 0xF000, opMove},
	},
	{ /* 0x4... */
		{0x4E70, 0xFFFF, opReset},      // RESET
		{0x4E71, 0xFFFF, opNop},        // NOP
		{0x4E72, 0xFFFF, opStop},       // STOP
		{0x4E73, 0xFFFF, opRte},        // RTE
		{0x4E75, 0xFFFF, opRts},        // RTS
		{0x4E76, 0xFFFF, opTrapv},      // TRAPV
		{0x4E77, 0xFFFF, opRtr},        // RTR
		{0x4E50, 0xFFF8, opLink},       // LINK
		{0x4E58, 0xFFF8, opUnlk},       // UNLK
		{0x4880, 0xFFF8, opExt},        // EXT.W
		{0x48C0, 0xFFF8, opExt},        // EXT.L
		{0x4840, 0xFFF8, opSwap},       // SWAP
		{0x4E40, 0xFFF0, opTrap},       // TRAP
		{0x4E60, 0xFFF0, opMoveUsp},    // MOVE USP
		{0x4E80, 0xFFC0, opJsr},        // JSR
		{0x4EC0, 0xFFC0, opJmp},        // JMP
		{0x4840, 0xFFC0, opPea},        // PEA
		{0x4AC0, 0xFFC0, opTas},        // TAS
		{0x40C0, 0xFFC0, opMoveFromSr}, // MOVE from SR
		{0x44C0, 0xFFC0, opMoveToCcr},  // MOVE to CCR
		{0x46C0, 0xFFC0, opMoveToSr},   // MOVE to SR
		{0x4880, 0xFB80, opMovem},      // MOVEM
		{0x41C0, 0xF1C0, opLea},        // LEA
		{0x4180, 0xF1C0, opChk},        // CHK.W
		{0x4800, 0xFFC0, opNbcd},       // NBCD
		{0x4000, 0xFF00, opNegx},       // NEGX
		{0x4200, 0xFF00, opClr},        // CLR
		{0x4400, 0xFF00, opNeg},        // NEG
		{0x4600, 0xFF00, opNot},        // NOT
		{0x4A00, 0xFF00, opTst},        // TST
	},
	{ /* 0x5... */
		{0x50C8, 0xF0F8, opDBcc}, // DBcc
		{0x50C0, 0xF0C0, opScc},  // Scc
		{0x5000, 0xF100, opAddq}, // ADDQ
		{0x5100, 0xF100, opSubq}, // SUBQ
	},
	{ /* 0x6... */
		{0x6000, 0xF000, opBcc},
	},
	{ /* 0x7... */
		{0x7000, 0xF100, opMoveq},
	},
	{ /* 0x8... */
		{0x80C0, 0xF1C0, opDivu}, // DIVU
		{0x81C0, 0xF1C0, opDivs}, // DIVS
		{0x8100, 0xF1F0, opSbcd}, // SBCD
		{0x8000, 0xF000, opOr},   // OR
	},
	{ /* 0x9... */
		{0x9000, 0xF000, opSub},
	},
	{ /* 0xA... */ },
	{ /* 0xB... */
		{0xB000, 0xF000, opCmpEor},
	},
	{ /* 0xC... */
		{0xC000, 0xF000, opAndMulAbcdExg},
	},
	{ /* 0xD... */
		{0xD000, 0xF000, opAdd},
	},
	{ /* 0xE... */
		{0xE000, 0xF000, opShift},
	},
	{ /* 0xF... */ },
}

func dispatch(op uint16) stepFunc {
	g := op >> 12
	for i := 0; i < len(opcodes[g]); i++ {
		if op&opcodes[g][i].mask == opcodes[g][i].base {
			return opcodes[g][i].f
		}
	}
	return nil
}
