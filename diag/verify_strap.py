#!/usr/bin/env python3
"""Verify key bytes in ROM."""
with open('/Users/jga/Development/goAmiga/external-cpu/goAmiga/Kick13.rom','rb') as f:
    rom = f.read()

print('=== Key address verification ===')
print(f'FE85C6: {rom[0x285C6:0x285CA].hex()}  expect 4eac000c = JSR $000C(A4)')
print(f'FE86EC: {rom[0x286EC:0x286EE].hex()}  expect 4ed0     = JMP (A0)')
print(f'FE86EE: {rom[0x286EE:0x286F0].hex()}  expect 4e75     = RTS')
print(f'FE844C: {rom[0x2844C:0x28452].hex()}  expect 47f900fe86ee = LEA $FE86EE,A3')
print(f'FE841C: {rom[0x2841C:0x28420].hex()}  expect 444f5300 = "DOS\\0"')

print('\n=== Boot block checksum algorithm at $FE85A4-$FE85C2 ===')
code = [
    (0x285A4, 2, 'MOVE.L (A4),D0           ; D0 = first longword of boot block'),
    (0x285A6, 4, 'CMP.L  (d16,PC),D0      ; compare with "DOS\\0" at $FE841C'),
    (0x285AA, 2, 'BNE.B  $FE8600           ; if not "DOS\\0", skip'),
    (0x285AC, 2, 'MOVEA.L A4,A0            ; A0 = boot block base address'),
    (0x285AE, 4, 'MOVE.W #$00FF,D1         ; D1 = 255 (DBRA loop = 256 iterations)'),
    (0x285B2, 2, 'MOVEQ  #0,D0             ; D0 = 0  (checksum accumulator)'),
    (0x285B4, 2, 'ADD.L  (A0)+,D0          ; add next longword'),
    (0x285B6, 2, 'BCC.B  $FE85BA           ; if no carry, skip'),
    (0x285B8, 2, 'ADDQ.L #1,D0             ; add carry back (ones complement!)'),
    (0x285BA, 4, 'DBRA   D1,$FE85B4        ; loop 256 times total'),
    (0x285BE, 2, 'NOT.L  D0                ; ones-complement the result'),
    (0x285C0, 2, 'BNE.B  $FE8600           ; if != 0, checksum FAILED'),
]
for off, sz, label in code:
    addr = 0xFC0000 + off
    hexb = rom[off:off+sz].hex()
    print(f'  ${addr:06X}: {hexb:12s}  {label}')

print('\n=== Boot block call and exit flow ===')
flow = [
    (0x285C2, 4, 'LEA    $002C(A5),A1      ; A1 = IORequest for trackdisk'),
    (0x285C6, 4, 'JSR    $000C(A4)          ; call boot block entry at offset $0C'),
    (0x285CA, 2, 'TST.L  D0                ; check boot block return code'),
    (0x285CC, 2, 'BEQ.B  $FE85F0           ; if D0=0 (success), continue'),
    (0,0,'--- error handling omitted ---'),
    (0x285F0, 2, 'MOVE.L A0,-(A7)          ; save A0 (returned by boot block!)'),
    (0x285F2, 6, 'JSR    $FE8C9C           ; cleanup subroutine'),
    (0x285F8, 2, 'MOVEA.L (A7)+,A0         ; restore boot blocks A0 return'),
    (0x285FA, 2, 'MOVEA.L A0,A3            ; A3 = boot block return value in A0'),
    (0x285FC, 4, 'BRA.W  $FE86B4           ; jump to exit code'),
]
for off, sz, label in flow:
    if off == 0:
        print(f'  {label}')
        continue
    addr = 0xFC0000 + off
    hexb = rom[off:off+sz].hex()
    print(f'  ${addr:06X}: {hexb:12s}  {label}')

print('\n=== Exit sequence at $FE86B4-$FE86EC ===')
exit_code = [
    (0x286B4, 4, 'BSR.W  $FE8984           ; cleanup subroutine'),
    (0x286B8, 6, 'MOVE.W #$8100,$DFF096    ; disable DMA/interrupts'),
    (0x286BE, 2, '...                      ; (more cleanup calls)'),
    (0x286D2, 2, 'MOVEA.L A4,A1            ; A1 = boot block buffer'),
    (0x286D4, 6, 'MOVE.L #$00000488,D0     ; D0 = size (1160 bytes)'),
    (0x286DA, 4, 'JSR    _LVOFreeMem(A6)   ; free boot block memory'),
    (0x286DE, 6, 'ADDA.L #$7E,A5           ; adjust stack frame'),
    (0x286E4, 2, 'UNLK   A5                ; unlink frame'),
    (0x286E6, 2, 'MOVEA.L A3,A0            ; A0 = boot block return value'),
    (0x286E8, 4, 'MOVEM.L (A7)+,D2/D3/A4/A5  ; restore saved regs'),
    (0x286EC, 2, 'JMP    (A0)              ; *** JUMP TO BOOT BLOCKS RETURN ***'),
    (0x286EE, 2, 'RTS                      ; (default A3 target if no boot)'),
]
for off, sz, label in exit_code:
    addr = 0xFC0000 + off
    hexb = rom[off:off+sz].hex()
    print(f'  ${addr:06X}: {hexb:12s}  {label}')

print('\n=== A3 initialization (default = RTS) ===')
print(f'  $FE844C: {rom[0x2844C:0x28452].hex()}  LEA $FE86EE,A3  ; default = RTS instruction')

print('\n=== ExecBase+$42 (BootPtr) in exec kernel at $FC6F44 ===')
exec_code = [
    (0x06F44, 4, 'MOVEA.L $0042(A6),A0     ; A0 = ExecBase->BootPtr (head of BootNode list)'),
    (0x06F48, 4, 'MOVE.L (A0),$0042(A6)    ; ExecBase->BootPtr = next node (dequeue)'),
    (0x06F4C, 2, 'BNE.B  $FC6F54           ; if new head != NULL'),
    (0x06F4E, 4, 'CLR.W  $00A2(A6)         ; clear SysBase->LastAlert[3]?'),
    (0x06F52, 2, 'BRA.B  $FC6F5E'),
    (0x06F54, 2, 'MOVEA.L (A0),A1          ; A1 = next boot node'),
    (0x06F56, 4, 'MOVE.W $000C(A1),D0      ; D0 = priority from next node'),
    (0x06F5A, 4, 'MOVE.W D0,$00A2(A6)      ; store it'),
]
for off, sz, label in exec_code:
    addr = 0xFC0000 + off
    hexb = rom[off:off+sz].hex()
    print(f'  ${addr:06X}: {hexb:12s}  {label}')

print('\n=== romboot.library call in strap at $FE852C ===')
rb_code = [
    (0x28524, 6, 'CMPA.L #$00000000,A2     ; is this first disk unit (A2=0)?'),
    (0x2852A, 2, 'BNE.B  $FE854A           ; if not first unit, skip romboot'),
    (0x2852C, 4, 'LEA    (PC,disp),A1      ; A1 = "romboot.library" string'),
    (0x28530, 2, 'MOVEQ  #0,D0             ; version 0'),
    (0x28532, 4, 'JSR    _LVOOpenLibrary(A6); open romboot.library'),
    (0x28536, 2, 'TST.L  D0                ; opened OK?'),
    (0x28538, 2, 'BEQ.B  $FE854A           ; no romboot, skip'),
    (0x2853A, 2, 'MOVE.L A6,-(A7)          ; save ExecBase'),
    (0x2853C, 2, 'MOVEA.L D0,A6            ; A6 = romboot library base'),
    (0x2853E, 4, 'JSR    -$1E(A6)          ; romboot.library function (manages BootNodes)'),
    (0x28542, 2, 'MOVEA.L A6,A1            ; A1 = romboot for CloseLibrary'),
    (0x28544, 2, 'MOVEA.L (A7)+,A6         ; restore ExecBase'),
    (0x28546, 4, 'JSR    _LVOCloseLibrary(A6)'),
]
for off, sz, label in rb_code:
    addr = 0xFC0000 + off
    hexb = rom[off:off+sz].hex()
    print(f'  ${addr:06X}: {hexb:12s}  {label}')
