// Package sepa creates SEPA Core direct-debit collection files (ISO 20022 pain.008.001.02) from invoices whose customers
// have signed a mandate, and records the collections once the bank reports them paid. It produces the file; sending it to the
// bank is done by the company through its bank's channel.
package sepa

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ibanRe       = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)
	bicRe        = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
	creditorIDRe = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{3}[A-Za-z0-9]{1,28}$`)
)

// NormalizeIBAN removes spaces, upper-cases and checks the ISO 13616 checksum (mod 97).
func NormalizeIBAN(s string) (string, error) {
	iban := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(s, " ", ""), "-", ""))
	if !ibanRe.MatchString(iban) {
		return "", errors.New("sepa: that does not look like an IBAN")
	}
	rearranged := iban[4:] + iban[:4]
	rem := 0
	for _, r := range rearranged {
		var v int
		switch {
		case r >= '0' && r <= '9':
			v = int(r - '0')
			rem = (rem*10 + v) % 97
		default:
			v = int(r-'A') + 10
			rem = (rem*100 + v) % 97
		}
	}
	if rem != 1 {
		return "", errors.New("sepa: the IBAN checksum is wrong, check for a typing mistake")
	}
	return iban, nil
}

// NormalizeBIC validates an optional BIC (empty is allowed, as SEPA no longer needs it within the euro area).
func NormalizeBIC(s string) (string, error) {
	b := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if b == "" {
		return "", nil
	}
	if !bicRe.MatchString(b) {
		return "", errors.New("sepa: that does not look like a BIC")
	}
	return b, nil
}

// ValidateCreditorID checks the shape of a SEPA creditor identifier (country, check digits, business code, national id).
func ValidateCreditorID(s string) (string, error) {
	c := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	if len(c) < 8 || len(c) > 35 || !creditorIDRe.MatchString(c) {
		return "", errors.New("sepa: that does not look like a SEPA creditor identifier (for Spain it looks like ES12ZZZ12345678)")
	}
	return c, nil
}

var translit = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'í': "i", 'ì': "i", 'î': "i", 'ï': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ú': "u", 'ù': "u", 'û': "u", 'ü': "u", 'ñ': "n", 'ç': "c", 'ß': "ss", 'ý': "y", 'ÿ': "y",
	'Á': "A", 'À': "A", 'Â': "A", 'Ä': "A", 'Ã': "A", 'Å': "A", 'É': "E", 'È': "E", 'Ê': "E", 'Ë': "E", 'Í': "I", 'Ì': "I", 'Î': "I", 'Ï': "I",
	'Ó': "O", 'Ò': "O", 'Ô': "O", 'Ö': "O", 'Õ': "O", 'Ú': "U", 'Ù': "U", 'Û': "U", 'Ü': "U", 'Ñ': "N", 'Ç': "C", 'æ': "ae", 'Æ': "AE", 'ø': "o", 'Ø': "O",
	'&': "+", '’': "'", '‘': "'", '“': "", '”': "", '€': "EUR", '–': "-", '—': "-",
}

// Clean reduces text to the SEPA character set (letters, digits and / - ? : ( ) . , ' + space) and a maximum length.
func Clean(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if t, ok := translit[r]; ok {
			b.WriteString(t)
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune("/-?:().,'+ ", r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if len(out) > max {
		out = strings.TrimSpace(out[:max])
	}
	return out
}

// Creditor is the company collecting the money.
type Creditor struct {
	Name       string
	IBAN       string
	BIC        string
	CreditorID string
}

// DebitItem is one direct debit.
type DebitItem struct {
	EndToEndID   string
	Amount       decimal.Decimal
	MandateRef   string
	MandateDate  time.Time
	DebtorName   string
	DebtorIBAN   string
	DebtorBIC    string
	Remittance   string
	SequenceType string // FRST or RCUR
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// BuildFile renders a pain.008.001.02 document for one collection date. Collections are grouped by sequence type, as the
// rulebook requires one payment-information block per sequence type.
func BuildFile(c Creditor, msgID string, createdAt time.Time, collectionDate time.Time, items []DebitItem) (string, decimal.Decimal, error) {
	if len(items) == 0 {
		return "", decimal.Zero, errors.New("sepa: no collections to include")
	}
	groups := map[string][]DebitItem{}
	total := decimal.Zero
	for _, it := range items {
		if it.SequenceType != "FRST" && it.SequenceType != "RCUR" {
			return "", decimal.Zero, fmt.Errorf("sepa: collection %s has no sequence type", it.EndToEndID)
		}
		if !it.Amount.IsPositive() || it.Amount.GreaterThan(decimal.RequireFromString("999999999.99")) {
			return "", decimal.Zero, fmt.Errorf("sepa: collection %s has an invalid amount", it.EndToEndID)
		}
		groups[it.SequenceType] = append(groups[it.SequenceType], it)
		total = total.Add(it.Amount)
	}
	seqs := make([]string, 0, len(groups))
	for k := range groups {
		seqs = append(seqs, k)
	}
	sort.Strings(seqs)

	var b strings.Builder
	w := func(s string) { b.WriteString(s) }
	name := esc(Clean(c.Name, 70))
	w(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	w(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pain.008.001.02"><CstmrDrctDbtInitn>`)
	w("<GrpHdr><MsgId>" + esc(msgID) + "</MsgId><CreDtTm>" + createdAt.UTC().Format("2006-01-02T15:04:05") + "</CreDtTm><NbOfTxs>" + fmt.Sprint(len(items)) + "</NbOfTxs><CtrlSum>" + total.StringFixed(2) + "</CtrlSum><InitgPty><Nm>" + name + "</Nm></InitgPty></GrpHdr>")
	for _, seq := range seqs {
		g := groups[seq]
		sum := decimal.Zero
		for _, it := range g {
			sum = sum.Add(it.Amount)
		}
		w("<PmtInf><PmtInfId>" + esc(msgID+"-"+seq) + "</PmtInfId><PmtMtd>DD</PmtMtd><BtchBookg>true</BtchBookg><NbOfTxs>" + fmt.Sprint(len(g)) + "</NbOfTxs><CtrlSum>" + sum.StringFixed(2) + "</CtrlSum>")
		w("<PmtTpInf><SvcLvl><Cd>SEPA</Cd></SvcLvl><LclInstrm><Cd>CORE</Cd></LclInstrm><SeqTp>" + seq + "</SeqTp></PmtTpInf>")
		w("<ReqdColltnDt>" + collectionDate.Format("2006-01-02") + "</ReqdColltnDt>")
		w("<Cdtr><Nm>" + name + "</Nm></Cdtr><CdtrAcct><Id><IBAN>" + c.IBAN + "</IBAN></Id></CdtrAcct>")
		if c.BIC != "" {
			w("<CdtrAgt><FinInstnId><BIC>" + c.BIC + "</BIC></FinInstnId></CdtrAgt>")
		} else {
			w("<CdtrAgt><FinInstnId><Othr><Id>NOTPROVIDED</Id></Othr></FinInstnId></CdtrAgt>")
		}
		w("<ChrgBr>SLEV</ChrgBr>")
		w("<CdtrSchmeId><Id><PrvtId><Othr><Id>" + esc(c.CreditorID) + "</Id><SchmeNm><Prtry>SEPA</Prtry></SchmeNm></Othr></PrvtId></Id></CdtrSchmeId>")
		for _, it := range g {
			w("<DrctDbtTxInf><PmtId><EndToEndId>" + esc(Clean(it.EndToEndID, 35)) + "</EndToEndId></PmtId>")
			w(`<InstdAmt Ccy="EUR">` + it.Amount.StringFixed(2) + "</InstdAmt>")
			w("<DrctDbtTx><MndtRltdInf><MndtId>" + esc(Clean(it.MandateRef, 35)) + "</MndtId><DtOfSgntr>" + it.MandateDate.Format("2006-01-02") + "</DtOfSgntr></MndtRltdInf></DrctDbtTx>")
			if it.DebtorBIC != "" {
				w("<DbtrAgt><FinInstnId><BIC>" + it.DebtorBIC + "</BIC></FinInstnId></DbtrAgt>")
			} else {
				w("<DbtrAgt><FinInstnId><Othr><Id>NOTPROVIDED</Id></Othr></FinInstnId></DbtrAgt>")
			}
			w("<Dbtr><Nm>" + esc(Clean(it.DebtorName, 70)) + "</Nm></Dbtr><DbtrAcct><Id><IBAN>" + it.DebtorIBAN + "</IBAN></Id></DbtrAcct>")
			w("<RmtInf><Ustrd>" + esc(Clean(it.Remittance, 140)) + "</Ustrd></RmtInf></DrctDbtTxInf>")
		}
		w("</PmtInf>")
	}
	w("</CstmrDrctDbtInitn></Document>")
	return b.String(), total, nil
}
