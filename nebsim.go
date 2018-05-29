package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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

type RefCnt struct {
	
}

type L3Ptbl struct {
	lvl  int
	ttes [] int64
}

type L2Ptbl struct {
	lvl  int
	ttes []*L3Ptbl
}

type L1Ptbl struct {
	lvl  int
	ttes []*L2Ptbl
}

type VvHdr struct {
	id    int
	name  string
	l1ptr [8]*L1Ptbl
	child int
}

var vvTbl []VvHdr

var headerDone int

func checkError(err error, msg string) {
	if err != nil {
		fmt.Println("%s err %d\n", msg, err)
		panic(err)
	}
}

func dumpL3Ptbl(vvid int, rootIdx int, l1idx int, l2idx int, l3ptbl *L3Ptbl) {
	//fmt.Printf("dumpL3Ptbl\n")
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

func dumpL2Ptbl(vvId int, rootIdx int, l1idx int, l2ptbl *L2Ptbl) {
	//fmt.Printf("dumpL2Ptbl\n")
	for i := 0; i < 1024; i++ {
		if l2ptbl.ttes[i] != nil {
			dumpL3Ptbl(vvId, rootIdx, l1idx, i, l2ptbl.ttes[i])
		}
	}
}

func dumpPtbl(vvId int, rootIdx int, l1ptbl *L1Ptbl) {
	//fmt.Printf("dumpPtbl\n")
	for i := 0; i < 1024; i++ {
		if l1ptbl.ttes[i] != nil {
			dumpL2Ptbl(vvId, rootIdx, i, l1ptbl.ttes[i])
		}
	}
}

func dumpRootPtrs(vvInd int) {
	//fmt.Printf("dumpRootPtrs\n")
	for i := 0; i < 8; i++ {
		if vvTbl[vvInd].l1ptr[i] != nil {
			fmt.Printf("vvid %d i %d not nil\n", vvTbl[vvInd].id, i);
		} else {
			fmt.Printf("vvid %d rootIdx %d nil\n", vvTbl[vvInd].id, i)
		}
	}
}

func dumpPtbls() {
	for i := 0; i < len(vvTbl); i++ {
		for j := 0; j < 8; j++ {
			if vvTbl[i].l1ptr[j] != nil {
				dumpPtbl(i, j, vvTbl[i].l1ptr[j])
			} else {
				// fmt.Printf("vvid %d rootIdx %d nil\n", i, j)
			}
		}
	}
}

func newL1Ptbl(lvl int) *L1Ptbl {
	retPtbl := new(L1Ptbl)
	retPtbl.lvl = lvl
	retPtbl.ttes = make([]*L2Ptbl, 1024, 1024)

	return retPtbl
}

func newL2Ptbl(lvl int) *L2Ptbl {
	retPtbl := new(L2Ptbl)
	retPtbl.lvl = lvl
	retPtbl.ttes = make([]*L3Ptbl, 1024, 1024)

	return retPtbl
}

func newL3Ptbl(lvl int) *L3Ptbl {
	retPtbl := new(L3Ptbl)
	retPtbl.lvl = lvl
	retPtbl.ttes = make([]int64, 1024, 1024)

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
 * 1 <addr> <len>
 */
func doIo(tokens []string) {

	var vhdr *VvHdr

	addr, err := strconv.ParseInt(tokens[1], 0, 64)
	checkError(err, "ParseInt addr")

	len, err := strconv.ParseInt(tokens[2], 0, 64)
	checkError(err, "ParseInt len")

	fmt.Printf("IO Addr: 0x%x Len: 0x%x\n", addr, len)

	pgAddr := dblk2pg(addr)

	l1, indexInL1 := l1idx(pgAddr)
	l2 := l2idx(pgAddr)
	l3 := l3idx(pgAddr)

	fmt.Printf("Addr: 0x%x L1: 0x%x IndexInL1: 0x%x L2: 0x%x L3: 0x%x\n", addr, l1, indexInL1, l2, l3)

	vhdr = &vvTbl[0]

	if vhdr.l1ptr[l1] == nil {
		//fmt.Printf("Need l1 tbl allocation idx %d\n", l1)
		vhdr.l1ptr[l1] = newL1Ptbl(1)
		//dumpRootPtrs(0)
		//fmt.Printf("Index %d in root set to %v\n", l1, vhdr.l1ptr[l1]);
	}

	l1Ptbl := vhdr.l1ptr[l1]

	if l1Ptbl.ttes[indexInL1] == nil {
		//fmt.Printf("Need l2 tbl allocation\n")
		l1Ptbl.ttes[indexInL1] = newL2Ptbl(2)
		//fmt.Printf("Index %d in l1Ptbl set to %v\n", indexInL1, l1Ptbl.ttes[indexInL1])
	}

	l2Ptbl := l1Ptbl.ttes[indexInL1]

	if l2Ptbl.ttes[l2] == nil {
		//fmt.Printf("Need l3 tbl allocation\n")
		l2Ptbl.ttes[l2] = newL3Ptbl(3)
		//fmt.Printf("Index %d in l2Ptbl set to %v\n", l2, l2Ptbl.ttes[l2])
	}

	l3Ptbl := l2Ptbl.ttes[l2]

	if l3Ptbl.ttes[l3] == 0 {
		l3Ptbl.ttes[l3] = addr
		//fmt.Printf("L3 idx %d set to addr %d\n", l3, addr)
	}
}

/*
 * 2 <svname>
 */
func createSv(tokens []string) {
}

/*
 * 3 <svname>
 */
func deleteSv(tokens []string) {
}

func doInit() {
	var newVhdr VvHdr

	newVhdr.id = 0
	newVhdr.name = "firstVv"

	vvTbl = append(vvTbl, newVhdr)
}

func printVv(vhdr VvHdr) {
	fmt.Printf("%16s : %d\n", "Id", vhdr.id)
	fmt.Printf("%16s : %s\n", "Name", vhdr.name)
	fmt.Printf("%16s : %d\n", "Child", vhdr.child)
}

func showVvs() {
	for i := 0; i < len(vvTbl); i++ {
		printVv(vvTbl[i])
	}
}

func main() {
	var err error

	doInit()

	reader := bufio.NewReader(os.Stdin)

	for err == nil {
		text, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		checkError(err, "ReadString ")

		tokens := strings.Fields(text)

		opCode, err := strconv.Atoi(tokens[0])
		checkError(err, "Atoi ")

		if opCode == 1 {
			doIo(tokens)
		} else if opCode == 2 {
			createSv(tokens)
		} else if opCode == 3 {
			deleteSv(tokens)
		} else if opCode == 4 {
			showVvs()
		} else if opCode == 5 {
			dumpPtbls()
		} else {
			panic("bad Opcode")
		}
	}
}
