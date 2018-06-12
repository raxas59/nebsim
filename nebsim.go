package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

/*
 * 64 TB vv requires 46 bits for byte address.
 * Each block is 512 bytes (9 bits).
 * Block address in 64 TB vv will be (46 - 9) = 37 bits
 * 8K page has 16 512 byte blocks, requring 4 bits for block within page
 * So 64 TB vv has 37 - 4 = 33 bits for page address.
 * Ptbl is 8k in size, each TTE is 8 bytes. So Each ptbl has 1k entries,
 * or needs a 10 bit index.
 * Out of 33 page address, 10 bits are L3 index, 10 bits are L2 index,
 * leaving 13 bits for l1 address. So we will have 8 l1 ptbls (3 bits),
 * and each L1 ptbl will have 1k entries (10 bit index).
 */
const INV_EXCEP = 0

type L3Ptbl struct {
	lvl  int
	refcnt	int
	ttes [] int64
}

type Ptbl struct {
	lvl  int
	refcnt	int
	ttes []*Ptbl
}

type VvHdr struct {
	id    int
	name  string
	l1ptr [8]*Ptbl
	child int
	parent int
}

var vvTbl []VvHdr

var headerDone int

var refMap map[uint64]int

var curVvId int

func checkError(err error, msg string) {
	if err != nil {
		fmt.Println("%s err %d\n", msg, err)
		panic(err)
	}
}

func ptblToPtblVal(curPtbl *Ptbl) uint64 {
	retVal := uint64(uintptr(unsafe.Pointer(curPtbl)))

	return (retVal)
}

func dumpL3Ptbl(vvid int, rootIdx int, l1idx int, l2idx int, l3ptbl *L3Ptbl) {
	for i := 0; i < 1024; i++ {
		if l3ptbl.ttes[i] != 0 {
			if (headerDone == 0) {
				fmt.Printf("%8s %8s %8s %8s %8s%12s\n", "VvId", "RootIdx", "L1Idx", "L2Idx", "L3Idx", "Addr")
				headerDone = 1
			}
			fmt.Printf("%8d 0x%-8x 0x%-8x 0x%-8x 0x%-8x 0x%-12x\n", vvid, rootIdx, l1idx, l2idx, i, l3ptbl.ttes[i])
		}
	}
}

func walkL2Ptbl(vvId int, rootIdx int, l1idx int, ptbl *Ptbl) {
    var l3ptbl *L3Ptbl
	for i := 0; i < 1024; i++ {
		if ptbl.ttes[i] != nil {
		    l3ptbl = (*L3Ptbl)(unsafe.Pointer(ptbl.ttes[i]))
			dumpL3Ptbl(vvId, rootIdx, l1idx, i, l3ptbl)
		}
	}
}

/*
 * vvId - id of vv whose ptbls are being printed.
 * rootIdx - index in root table that we are printing.
 */
func walkL1Ptbl(vvId int, rootIdx int, ptbl *Ptbl) {
	for i := 0; i < 1024; i++ {
		if ptbl.ttes[i] != nil {
			walkL2Ptbl(vvId, rootIdx, i, ptbl.ttes[i])
		}
	}
}

func dumpPtbls() {
	for i := 1; i < len(vvTbl); i++ {
		for j := 0; j < 8; j++ {
			if vvTbl[i].l1ptr[j] != nil {
				walkL1Ptbl(i, j, vvTbl[i].l1ptr[j])
			}
		}
	}
}

func copyPtbl(curPtbl *Ptbl) *Ptbl {
	retPtbl := new(Ptbl)
	retPtbl.lvl = curPtbl.lvl
	retPtbl.ttes = make([]*Ptbl, 1024, 1024)

	for i := 0; i < 1024; i++ {
		retPtbl.ttes[i] = curPtbl.ttes[i]
	}

	return retPtbl
}

func newPtbl(lvl int) *Ptbl {
	retPtbl := new(Ptbl)
	retPtbl.lvl = lvl
	retPtbl.ttes = make([]*Ptbl, 1024, 1024)

	return retPtbl
}

/*
 * Given a block address, return the page
 * address (each page is 8k)
 */
func dblk2pg(blkaddr int64) int64 {
	ret := (blkaddr >> 4)

	return (ret)
}

func l3idx(pgaddr int64) int {
	ret := pgaddr & 0x3ff

	return (int(ret))
}

func l2idx(pgaddr int64) int {
	ret := (pgaddr >> 10) & 0x3ff

	return (int(ret))
}

func decrRef(ptblAddr uint64) {
	val := refMap[ptblAddr]
	if val <= 1 {
		fmt.Printf("ptblAddr %v bad refcount %v\n", ptblAddr, val)
		panic("incrRef")
	}
	refMap[ptblAddr] = val - 1
}

func incrRef(ptblAddr uint64) {
	val := refMap[ptblAddr]
	if val == 0 {
		fmt.Printf("ptblAddr %v doesn't exist\n", ptblAddr)
		panic("incrRef")
	}
	refMap[ptblAddr] = val + 1
}

/*
 * We need to return a new ptbl which is a copy of this ptbl.
 * new ptbl will have a refcount of 1.
 * refcount of pagetable being passed in will get decremented by 1
 * Also need to increment the refcounts of all the ptbls pointed
 * to by this ptbl unless this is a level 3 ptbl.
 */
func doCow(curPtbl *Ptbl) *Ptbl {

	fmt.Printf("doCow for lvl %d\n", curPtbl.lvl)

	retPtbl := copyPtbl(curPtbl)

	if curPtbl.lvl == 3 {
		return (retPtbl)
	}

	for i := 0; i < 1024; i++ {
		ptbl := curPtbl.ttes[i]
		ptblVal := ptblToPtblVal(ptbl)
		if ptblVal != 0 {
			incrRef(ptblVal)
		}
	}

	curPtblVal := ptblToPtblVal(curPtbl)
	decrRef(curPtblVal)

	return (retPtbl)
}

/*
 * l1idx returns a 3 bit index to pick one of
 * eight l1 ptbls, and a 10 bit index into that
 * ptbl
 */
func l1idx(pgaddr int64) (int, int) {
	var retl1idx int
	var retPtblIdx int

	ret := (pgaddr >> 20)

	retl1idx = int(ret) & 0x3FF

	retPtblIdx = (int(ret) >> 10)

	return retPtblIdx, retl1idx
}

/*
 * 2 <vvid> <addr> <len>
 */
func doRead(tokens []string) {

	var vhdr *VvHdr
	var l3Ptbl *L3Ptbl
	var vvId int64

	vvId, err := strconv.ParseInt(tokens[1], 0, 64)
	checkError(err, "ParseInt addr")

	addr, err := strconv.ParseInt(tokens[2], 0, 64)
	checkError(err, "ParseInt addr")

	len, err := strconv.ParseInt(tokens[3], 0, 64)
	checkError(err, "ParseInt len")

	fmt.Printf("Read vvId: %d Addr: 0x%x Len: 0x%x\n", vvId, addr, len)

	pgAddr := dblk2pg(addr)

	l1, indexInL1 := l1idx(pgAddr)
	l2 := l2idx(pgAddr)
	l3 := l3idx(pgAddr)

	fmt.Printf("Addr: 0x%x L1: 0x%x IndexInL1: 0x%x L2: 0x%x L3: 0x%x\n", addr, l1, indexInL1, l2, l3)

	vhdr = &vvTbl[vvId]

	if vhdr.l1ptr[l1] == nil {
		fmt.Printf("Read vvId: %d Addr: 0x%x Len: 0x%x Index l1: %d nil\n", vvId, addr, len, l1)
		return
	}

	l1Ptbl := vhdr.l1ptr[l1]

	if l1Ptbl.ttes[indexInL1] == nil {
		fmt.Printf("Read vvId: %d Addr: 0x%x Len: 0x%x Index in l1: %d nil\n", vvId, addr, len, indexInL1)
		return
	}

	l2Ptbl := l1Ptbl.ttes[indexInL1]

	if l2Ptbl.ttes[l2] == nil {
		fmt.Printf("Read vvId: %d Addr: 0x%x Len: 0x%x Index in l2: %d nil\n", vvId, addr, len, l2)
		return
	}

	l3Ptbl = (*L3Ptbl)(unsafe.Pointer(l2Ptbl.ttes[l2]))

	val := l3Ptbl.ttes[l3]

	fmt.Printf("Addr: 0x%x L1: 0x%x IndexInL1: 0x%x L2: 0x%x L3: 0x%x val: 0x%x\n", addr, l1, indexInL1, l2, l3, val)
}

/*
 * 1 <vvid> <addr> <len> <val>
 */
func doWrite(tokens []string) {

	var vhdr *VvHdr
	var l3Ptbl *L3Ptbl
	var ptblVal uint64
	var nPtbl *Ptbl
	var vvId int64

	vvId, err := strconv.ParseInt(tokens[1], 0, 64)
	checkError(err, "ParseInt addr")

	addr, err := strconv.ParseInt(tokens[2], 0, 64)
	checkError(err, "ParseInt addr")

	len, err := strconv.ParseInt(tokens[3], 0, 64)
	checkError(err, "ParseInt len")

	val, err := strconv.ParseInt(tokens[4], 0, 64)
	checkError(err, "ParseInt val")

	fmt.Printf("Write Addr: vvId: %d 0x%x Len: 0x%x Val: 0x%x\n", vvId, addr, len, val)

	pgAddr := dblk2pg(addr)

	l1, indexInL1 := l1idx(pgAddr)
	l2 := l2idx(pgAddr)
	l3 := l3idx(pgAddr)

	fmt.Printf("Addr: 0x%x L1: 0x%x IndexInL1: 0x%x L2: 0x%x L3: 0x%x\n", addr, l1, indexInL1, l2, l3)

	vhdr = &vvTbl[vvId]

	if vhdr.l1ptr[l1] == nil {
		vhdr.l1ptr[l1] = newPtbl(1)
		ptblVal = ptblToPtblVal(vhdr.l1ptr[l1])
		refMap[ptblVal] = 1
	}

	l1Ptbl := vhdr.l1ptr[l1]
	l1PtblVal := ptblToPtblVal(l1Ptbl)
	if refMap[l1PtblVal] > 1 {
		/*
		 * Need to split this ptbl
		 */
		 nPtbl = doCow(l1Ptbl)
		 l1Ptbl = nPtbl
		 vhdr.l1ptr[l1] = l1Ptbl
	}

	if l1Ptbl.ttes[indexInL1] == nil {
		l1Ptbl.ttes[indexInL1] = newPtbl(2)
		ptblVal = ptblToPtblVal(l1Ptbl.ttes[indexInL1])
		refMap[ptblVal] = 1
	}

	l2Ptbl := l1Ptbl.ttes[indexInL1]
	l2PtblVal := ptblToPtblVal(l2Ptbl)
	if refMap[l2PtblVal] > 1 {
		nPtbl = doCow(l2Ptbl)
		l2Ptbl = nPtbl
		l1Ptbl.ttes[indexInL1] = l2Ptbl
	}

	if l2Ptbl.ttes[l2] == nil {
		l2Ptbl.ttes[l2] = newPtbl(3)
		ptblVal = ptblToPtblVal(l2Ptbl.ttes[l2])
		refMap[ptblVal] = 1
	}

	l3Ptbl = (*L3Ptbl)(unsafe.Pointer(l2Ptbl.ttes[l2]))
	l3PtblVal := ptblToPtblVal(l2Ptbl.ttes[l2])
	if refMap[l3PtblVal] > 1 {
		nPtbl = doCow(l2Ptbl.ttes[l2])
		l3Ptbl = (*L3Ptbl)(unsafe.Pointer(nPtbl))
		l2Ptbl.ttes[l2] = nPtbl
	}

	l3Ptbl.ttes[l3] = val

	fmt.Printf("Setting l3 index %d to val %d\n", l3, val)
}

/*
 * 3 <svname>
 */
func createSv(tokens []string) {
	var newVhdr VvHdr
	var parVhdr *VvHdr

	newVhdr.id = curVvId
	newVhdr.name = tokens[1]
	newVhdr.parent = 1

	parVhdr = &vvTbl[1]

	parVhdr.child = curVvId

	for i := 0; i < 8; i++ {
		newVhdr.l1ptr[i] = parVhdr.l1ptr[i]

		if newVhdr.l1ptr[i] != nil {
			l1ptbl := newVhdr.l1ptr[i]
			l1ptblAddr := ptblToPtblVal(l1ptbl)
			incrRef(l1ptblAddr)
		}
	}

	vvTbl = append(vvTbl, newVhdr)

	curVvId++
}

/*
 * 4 <svname>
 */
func deleteSv(tokens []string) {
}

func doInit() {
	var newVhdr VvHdr

	newVhdr.id = 0
	newVhdr.name = "badVv"

	vvTbl = append(vvTbl, newVhdr)

	newVhdr.id = 1
	newVhdr.name = "myVv"

	vvTbl = append(vvTbl, newVhdr)

	refMap = make(map[uint64]int)

	curVvId = 2
}

func printVv(vhdr VvHdr) {
	fmt.Printf("%16s : %d\n", "Id", vhdr.id)
	fmt.Printf("%16s : %s\n", "Name", vhdr.name)
	fmt.Printf("%16s : %d\n", "Child", vhdr.child)
	fmt.Printf("%16s : %d\n\n", "Parent", vhdr.parent)
}

func showVvs() {
	for i := 1; i < len(vvTbl); i++ {
		printVv(vvTbl[i])
	}
}

func printPrompt() {
	fmt.Printf("\n")
	fmt.Printf("doWrite		: 1 <vvid> <addr> <len> <val>\n")
	fmt.Printf("doRead		: 2 <vvid> <addr> <len>\n")
	fmt.Printf("createSv	: 3 <svname>\n")
	fmt.Printf("deleteSv	: 4 <svname>\n")
	fmt.Printf("showVvs		: 5\n")
	fmt.Printf("dumpPtbls	: 6\n")
	fmt.Printf(">>> ")
}

func main() {
	var err error

	doInit()

	reader := bufio.NewReader(os.Stdin)

	for err == nil {
		printPrompt()
		text, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		checkError(err, "ReadString ")

		tokens := strings.Fields(text)

		opCode, err := strconv.Atoi(tokens[0])
		checkError(err, "Atoi ")

		if opCode == 1 {
			doWrite(tokens)
		} else if opCode == 2 {
			doRead(tokens)
		} else if opCode == 3 {
			createSv(tokens)
		} else if opCode == 4 {
			deleteSv(tokens)
		} else if opCode == 5 {
			showVvs()
		} else if opCode == 6 {
			dumpPtbls()
		} else {
			panic("bad Opcode")
		}
	}
}
