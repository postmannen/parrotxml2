/*The idea here is to create a lexer for the parrot.xml file which
holds the definition of the protocol used to control the Parrot Bebop 2 drone.
The lexer will be build't by having one main run function who executes a function,
and get a new function in return, that again will be executed next.
The program will be build't up by many smaller functions who serve one single purpose
in the production line, and they know what function to return next based on they're own
simple logic.
All tag lines spanning several lines will be concatenated into a single line to make the
lexing easier. The concatenation will make sure that all the lines being lexed have a start
and an end lile <a></a>, or <a/>.
*/

// TODO: Make a package, and move main into /cmd
// TODO: Make main accept command line arguments to choose output to channel or console
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

//tagType is used for storing if we are at the start or stop tag position while lexing a line.
// This is used for choosing to send start or stop token with tag name as value.
type tagType int

const (
	startTag tagType = iota
	stopTag
)

type lexer struct {
	fileReader      *bufio.Reader //buffer used for reading file lines.
	currentLineNR   int           //the line nr. being read
	currentLine     string        //the current single line we mainly work on.
	nextLine        string        //the line after the current line.
	EOF             bool          //used for proper ending of readLine method.
	workingLine     string        //the line being worked on. Can be a collection of several lines.
	workingPosition int           //the chr position we're currently working at in line
	firstLineFound  bool          //used to tell if a start tag was found so we can separate descriptions (which have to tags) from the rest while making the lexer.workingLine.
	foundEqual      bool          //used to detect if a line contains any argument/attributes, or just text
	tagName         string        //to store the name of the tag while lexing a line.
	tagTypeIs       tagType       //used to indicate if we are currently working on a start or a stop tag.
	sendToken       tokenSender   //used to attach the tokenSender function. Must be initialized in main.
}

//newLexer will return a *lexer type, it takes an io.Reder as input, which can be f.ex. a file handler.
func newLexer(fh io.Reader, to tokenOutputType) *lexer {
	return &lexer{
		fileReader:    bufio.NewReader(fh),
		currentLineNR: -1,
		sendToken:     newTokenSender(to),
	}
}

//stateFunc is the defenition of a state function.
type stateFunc func() stateFunc

//lexReadFileLine will allways read the next line, and move the previous next line
// into current line on next run. All spaces and carriage returns are removed.
// Since we are working on the line that was read on the prevoius run, we will
// set l.EOF to true as our exit parameter if error==io.EOF, so the whole
// function is called one more time if error=io.EOF so we also get the last line
// of the file moved into l.currentLine.
func (l *lexer) lexReadFileLine() stateFunc {
	l.workingPosition = 0
	l.currentLineNR++
	l.currentLine = l.nextLine
	line, _, err := l.fileReader.ReadLine()
	if err != nil {
		if l.EOF {
			l.sendToken(tokenEOF, "EOF")
			//TODO: This close does not work with testing, works ok in normal run!
			close(tokenChan)
			return nil
		}
		if err == io.EOF {
			l.EOF = true
		}
	}
	l.nextLine = strings.TrimSpace(string(line))
	return l.lexCheckLineType
}

//lexStart will start the reading of lines from file, and then kickstart it all
// by running the returned function inside the for loop.
// Since all methods return a new method to be executed on the next run, we
// will check if the current ran method returned nil instead of a new method
// to exit.
func (l *lexer) lexStart() {

	fn := l.lexReadFileLine()
	for {
		fn = fn()
		if fn == nil {
			break
		}
	}
}

//lexPrint will print the current working line.
func (l *lexer) lexPrint() stateFunc {
	fmt.Printf("\n Line nr=%v, %v\n", l.currentLineNR, l.workingLine)
	fmt.Println("-------------------------------------------------------------------------")
	//We reset variables here, since this is the last link in the chain of functions.
	l.workingLine = ""
	return l.lexReadFileLine()
}

//lexLineContent will work itselves one character position at a time the string line,
// and do some action based on the type of character found.
// Checking if a line is done with lexing is done here by checking if working
// position < len. If greater we're done lexing the line, and can continue with
// the next operation.
//
//All functions returned from this function will return back here to continue the iteration
// of the specific line until the end of line, then it will chose the last return at the
// bottom of the function, and get out of the looping back into this function for the specific
// line
//
func (l *lexer) lexLineContent() stateFunc {
	//Check all the individual characters of the string
	//
	for l.workingPosition < len(l.workingLine) {
		switch l.workingLine[l.workingPosition] {
		case '<':
			//check if it is an end tag which starts with </
			if l.workingLine[l.workingPosition+1] == '/' {
				//If there was no attributes, there are likely to be a text string between the tags.
				// Check for it !
				if !l.foundEqual {
					t := l.lexTextBetweenTags()
					//If some text was returned
					if t != "" {
						l.sendToken(tokenJustText, t)
					}
				}

				l.tagTypeIs = stopTag
				return l.lexTagName //find tag name
				//break //found end, no need to check further, break out.
			}

			//It was a start tag
			l.tagTypeIs = startTag
			return l.lexTagName //find tag name
		case '>':
			if strings.Contains(l.workingLine, "/>") {
				l.sendToken(tokenEndTag, l.tagName)
			}
			if strings.Contains(l.workingLine, "?>") {
				l.sendToken(tokenEndTag, l.tagName)
			}
		case '=':
			l.foundEqual = true
			// fmt.Println("------FOUND EQUAL SIGN CHR----------")
			l.sendToken(tokenArgumentFound, "found argument")
			return l.lexTagArguments
		}

		l.workingPosition++
	}

	l.foundEqual = false
	l.tagName = ""
	return l.lexPrint
}

//lexStartStopTag will check various versions of tags. Like if is a start tag, stop tag
// tag with elements inside.
func (l *lexer) lexTextBetweenTags() (elementText string) {
	//If no equal was detected in the line, it is most likely a line with a start,
	// and an end tag, but just text inbetween, and we want to pick out that text.
	// Example : <someTag>WE WANT THIS TEXT</someTag>
	if !l.foundEqual {
		pos := 0
		var posTextStart int
		var posTextStop int
		var text []byte

		for {
			if pos >= len(l.workingLine) {
				break
			}
			switch {
			case l.workingLine[pos] == '<' && pos == 0:
				//fmt.Println("-- firstStartAngle found", firstStartAngleFound)
			case l.workingLine[pos] == '>' && pos == len(l.workingLine)-1:
				//fmt.Println("-- secondStopAngle found", secondStopAngleFound)
			case l.workingLine[pos] == '>':
				posTextStart = pos + 1
				//fmt.Println("-- firstStopAngle found", firstStopAngleFound)
			case l.workingLine[pos] == '<':
				posTextStop = pos - 1
				//fmt.Println("-- secondStartAngle found", secondStartAngleFound)
			default:
				//if there are more angle brackets than needed for a start and end tag
				// there is something malformed in xml, and we break out
				if l.workingLine[pos] == '<' || l.workingLine[pos] == '>' {
					log.Println("error: malformed xml with to man angle brackets")
				}
			}

			pos++
		}

		if posTextStart != 0 || posTextStop != 0 {
			for i := posTextStart; i <= posTextStop; i++ {
				text = append(text, l.workingLine[i])
			}
			return string(text)
		}

	}
	return ""
}

//lexTagArguments will pick out all the arguments "arg=value".
func (l *lexer) lexTagArguments() stateFunc {
	p1 := findChrPositionBefore(l.workingLine, ' ', l.workingPosition)
	arg := findLettersBetween(l.workingLine, p1, l.workingPosition)
	l.sendToken(tokenArgumentName, arg)

	//we add +1 to the working position below so we don't search and exit on the start quote.
	p2 := findChrPositionAfter(l.workingLine, '"', l.workingPosition+1)
	value := findLettersBetween(l.workingLine, l.workingPosition+1, p2)
	l.sendToken(tokenArgumentValue, value)

	l.workingPosition++
	return l.lexLineContent
}

//findChrPositionBefore .
// Searches backwards in a string from a given positions,
// for the first occurence of a character.
//
func findChrPositionBefore(s string, preChr byte, origChrPosition int) (preChrPosition int) {
	p := origChrPosition
	//find the first space before the preceding word
	for {
		p--
		if p < 0 {
			log.Println("Found no space before the equal sign, reached the beginning of the line")
			break
		}
		if s[p] == preChr {
			preChrPosition = p
			break
		}
	}

	//Will return the position of the prior occurance of the a the character
	return
}

//findChrPositionAfter .
// Searches forward in a string from a given positions,
// for the first occurence of a character after it.
//
func findChrPositionAfter(s string, preChr byte, origChrPosition int) (nextChrPosition int) {
	p := origChrPosition
	//find the first space before the preceding word
	for {
		p++
		if p > len(s)-1 {
			log.Println("Found no space before the equal sign, reached the end of the line")
			break
		}

		//When value is the last thing in a line, it will be followed by '>' and not a space.
		if s[p] == preChr || s[p] == '>' {
			nextChrPosition = p
			break
		}
	}

	//will return the preceding chr's positions found
	return
}

//findLettersBetween
// takes a string, and two positions given as slices as input,
// and returns a slice of string with the words found.
//'
func findLettersBetween(s string, firstPosition int, secondPosition int) (word string) {
	letters := []byte{}
	//as long as first pos'ition is lower than second position....
	for firstPosition < secondPosition {
		letters = append(letters, s[firstPosition])
		firstPosition++
	}
	word = string(letters)
	word = strings.Trim(word, "\"")

	return
}

//lexTagName looks for the tag name in that line
// check for the first space, and grab the letters between < and space for tag name.
func (l *lexer) lexTagName() stateFunc {
	//var start bool
	//var end bool
	var tn []byte

	l.workingPosition++
	//....check if there is a / following the <, then it is an end tag.
	if l.workingLine[l.workingPosition] == '/' {
		l.workingPosition++
	}

	for {
		//Look for space.  The name ends where the space is.
		if l.workingLine[l.workingPosition] == ' ' {
			break
		}
		if l.workingLine[l.workingPosition] == '>' {
			break
		}

		//if none of the above, we can safely add the chr to the slice
		tn = append(tn, l.workingLine[l.workingPosition])
		l.workingPosition++
	}

	l.tagName = string(tn)

	switch l.tagTypeIs {
	case startTag:
		l.sendToken(tokenStartTag, l.tagName)
	case stopTag:
		l.sendToken(tokenEndTag, l.tagName)
	}

	//we return lexLineContent since we know we want to check if there is more to do with the line
	return l.lexLineContent
}

//lexCheckLineType checks what kind of line we are dealing with. If the line belongs
// together with the line following after, the lines will be combined into a single
// string to make it clearer what belongs to a given tag while lexing.
// If string is blank, or end of string is reached we exit, and read a new line.
func (l *lexer) lexCheckLineType() stateFunc {
	// If the line is blank, return and read a new line
	if len(l.currentLine) == 0 {
		//log.Println("NOTE ", l.currentLineNR, ": blank line, getting out and reading the next line")
		return l.lexReadFileLine
	}

	start := strings.HasPrefix(l.currentLine, "<")
	end := strings.HasSuffix(l.currentLine, ">")
	nextLineStart := strings.HasPrefix(l.nextLine, "<")

	//TAG: set the workingLine = currentLine and go directly to lexing.
	if start && end {
		l.firstLineFound = false
		l.workingLine = l.currentLine
		return l.lexLineContent
	}

	// TAG: This indicates this is the first line with a start tag, and the rest are on the following lines.
	// Set initial workingLine=currentLine, and read another line. We set l.firstLineFound to true, to signal
	// that we want to add more lines later to the current working line.
	//
	// Have start, no end, continues on the next line
	if start && !end {
		l.firstLineFound = true
		l.workingLine = l.workingLine + " " + l.currentLine
		return l.lexReadFileLine
	}

	// TAG: This indicates we have found a start earlier, and that we need to add this currentLine to the
	// existing content of the workingLine, and read another line
	if !start && !end && l.firstLineFound {
		l.workingLine = l.workingLine + " " + l.currentLine
		return l.lexReadFileLine
	}

	//TAG: This should indicate that we found the last line of several that have to be combined
	if !start && end && l.firstLineFound {
		l.workingLine = l.workingLine + " " + l.currentLine
		l.firstLineFound = false //end found, set firstLineFound back to false to be ready for finding new tag.
		return l.lexLineContent
	}

	//Description line. These are lines that have no start or end tag that belong to them.
	// Starts, but continues on the next line.
	if !start && !end && !l.firstLineFound && !nextLineStart {
		l.workingLine = l.workingLine + " " + l.currentLine
		return l.lexReadFileLine
	}

	//Description line. These are lines that have no start or end tag that belong to them.
	// End's here.
	//TODO: This one should not need to lexLineContent since there is just descriptions here,
	// no need to lex inside, and it should be given a token immediately.
	if !start && !end && !l.firstLineFound && nextLineStart {
		l.workingLine = l.workingLine + " " + l.currentLine
		l.sendToken(tokenDescription, l.workingLine)
		return l.lexLineContent
	}

	// ---------------------The code should never exexute the code below-----------------------
	// The print and return below should optimally never happen.
	// Check it's output to figure what is not detected in the if's above.
	fmt.Println("DEBUG: *uncaught line!! :", l.currentLineNR, l.workingLine)
	return l.lexPrint
}

//------------------------------------Tokens--------------------------------------------
/*
Example for how to send and receive tokens from the lexer.
When the lexer find something, it creates a token of type token. It will choose the type of token found and put that into tokenType, and the text found will be put into the argument.
Then the token is put on the channel to be received by the parser, and the go struct code will be generated within the switch/case selection.
*/

//tokenType is the type describing a token.
// A <start> start tag will have token start.
// An </start> end tag will have token end.
type tokenType string

//XML tag - element - node, 3 names for the same thing.
const (
	tokenStartTag      tokenType = "tokenStartTag"
	tokenEndTag        tokenType = "tokenEndTag"
	tokenArgumentFound tokenType = "tokenArgumentFound"
	tokenArgumentName  tokenType = "tokenArgumentName"
	tokenArgumentValue tokenType = "tokenArgumentValue"
	tokenDescription   tokenType = "tokenDescription"
	tokenEOF           tokenType = "tokenEOF"
	tokenJustText      tokenType = "tokenJustText"
)

type token struct {
	tokenType        //type of token, tokenStart, tokenStop, etc.
	tokenText string //the actual text found in the xml while lexing
}

//readToken will pickup and read all the tokens that is received from the lexer
func readToken() {
	for v := range tokenChan {
		fmt.Println("*readToken from channel * ", v.tokenType, ", tokenText = ", v.tokenText)
	}
	wg.Done()
}

type tokenSender func(tokenType, string)

func tokenSendChannel(tType tokenType, tText string) {
	tokenChan <- token{tokenType: tType, tokenText: tText}
}

//tokenSendConsole sends the token to STDOUT for printing
func tokenSendConsole(tType tokenType, tText string) {
	fmt.Printf("* %v, tokenText = %v\n", tType, tText)
}

type tokenOutputType int

const (
	console tokenOutputType = iota
	channel
)

//newTokenSender will return a function that will send the output to either "console" or "channel"
// based on what is chosen as input.
func newTokenSender(t tokenOutputType) tokenSender {
	switch t {
	case console:
		return tokenSendConsole
	case channel:
		return tokenSendChannel
	}

	return nil
}

//------------------------------------------------------------------------------------------

var tokenChan chan token
var wg sync.WaitGroup

func main() {

	tokenChan = make(chan token)

	a := os.Args
	if len(a) < 2 {
		log.Fatal("Specify an xml file\n")

	}

	fileName := flag.String("fileName", "", "specify the filename to check")
	tokenOutput := flag.Int("tokenOutput", 0, "specify '0' for console or '1' for channel. If you want to simulate a read locally without a parser who picks up the data from the channel, remember to enable -readChannel=yes.")
	//readChannel := flag.String("readChannel", "no", "yes/no , to enable a read channel to consume/read the data that is put on the channel. This is only used for if testing locally without a parser who read from the channel.")

	flag.Parse()

	fh, err := os.Open(*fileName)
	if err != nil {
		log.Fatal("Error: opening file: ", err)
	}

	//we need to start a consumer for reading the tokens put on the channel,
	// if channel is chosen as output.
	if *tokenOutput == 1 {
		go readToken()
		wg.Add(1)
		defer wg.Wait()
	}

	lex := newLexer(fh, tokenOutputType(*tokenOutput))
	lex.lexStart()

}
