package main

import (
	"os"
	"bufio"
	"fmt"
	"strings"
	"strconv"
)

type Excep struct {
	excepType	int
	excepAddr	int64
}

type Ptbl struct {
	lvl		int
	ttes	*Excep
}

type VvHdr struct {
	id		int
	name	string
	l1ptr	*Ptbl
}

func checkError(err error, msg string) {
	if err != nil {
		fmt.Println("%s err %d\n", msg, err)
		panic(err)
	}
}

/*
 * 1 <addr> <len>
 */
func doIo(tokens [] string) {
	addr, err := strconv.ParseInt(tokens[1], 0, 64)
	checkError(err, "ParseInt addr")

	len, err := strconv.ParseInt(tokens[2], 0, 64)
	checkError(err, "ParseInt len")

	fmt.Printf("IO Addr: 0x%x Len: 0x%x\n", addr, len)
}

/*
 * 2 <svname>
 */
func createSv(tokens [] string) {
}

/*
 * 3 <svname>
 */
func deleteSv(tokens [] string) {
}

func main() {
	var err error

	reader := bufio.NewReader(os.Stdin)

	for err == nil {
		text, err := reader.ReadString('\n')
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
		} else {
			panic("bad Opcode")
		}
    }
}
