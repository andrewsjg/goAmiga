# m68k

The 680x0 CPU of [goAmiga](../README.md): a pure-Go Motorola 68000, 68020
and 68030 (with its MMU, and a 68881/68882 FPU), cycle-timed, with the
exception frames, trace mode and prefetch behaviour that Amiga software
relies on. The 68030's MMU is in `mmu030.go`: table walks, transparent
translation, the U and M bits, and bus errors that rerun the faulting
instruction after the fault is fixed.

It began as Ryan Armstrong's [go-m68k](https://github.com/cavaliercoder/go-m68k)
(BSD licence, see LICENSE). It was developed on a branch for goAmiga, and
in October 2026 it was merged into the goAmiga module, history included.
Ryan's aim was a 68000 emulator to learn from, rather than a rival to the
established ones; the sim/, srec/ and dump/ packages and the cmd/m68k tool
for running assembled programs come from that original project.

## Use

```go
import "github.com/andrewsjg/goamiga/m68k"
```

The SingleStepTests 68000 suite runs when its data is in
`testdata/680x0/` (see `singlestep_test.go`); it's ~190 MB, so it's not in
git.

## Example

Compile the following Motorola 68000 assembly using a supported compiler:

```plain
* Test 68000 simulator program

WRCHAR  EQU     6                       Trap # for write D1.B char to screen

        ORG $1000

        LEA      MESSAGE(PC),A0         Point A0 to start of message
NEXT    MOVE.B  (A0)+,D1                Get character, increment pointer
        BEQ.S   FINISH                  Exit if end
        MOVE.B  #WRCHAR,D0              Set up trap to write to screen
        TRAP    #15                     Write char. to screen
        BRA.S   NEXT                    ..and loop back
FINISH  STOP    #$2700                  Halt
MESSAGE DC.B    'Hello world!',$0D,$0A,0

        END     $1000
```

Run the program:

```plain
$ m68k hello-world.h68
Hello world!
```
