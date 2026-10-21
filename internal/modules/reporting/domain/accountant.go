package domain

import (
	"errors"
	"time"
)

var ErrAccountantCompanyNotFound = errors.New("accountant export: company not found")

// AccountantData is one database-statement snapshot for one registration/company.
// Decimal values remain strings through JSON and file generation.
type AccountantData struct {
	Company    AccountantCompany    `json:"company"`
	Documents  []AccountantDocument `json:"documents"`
	Lines      []AccountantLine     `json:"lines"`
	SnapshotAt time.Time            `json:"snapshot_at"`
}
type AccountantCompany struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Country             string   `json:"country"`
	Currency            string   `json:"currency"`
	TaxID               string   `json:"tax_id"`
	State               string   `json:"state"`
	USCollectionRegions []string `json:"us_collection_regions"`
}
type AccountantDocument struct {
	ID             string `json:"id"`
	Direction      string `json:"direction"`
	Type           string `json:"type"`
	Number         string `json:"number"`
	Date           string `json:"date"`
	SupplyDate     string `json:"supply_date"`
	Status         string `json:"status"`
	Party          string `json:"party"`
	PartyTaxID     string `json:"party_tax_id"`
	Reference      string `json:"reference"`
	SupplierNumber string `json:"supplier_number"`
	SupplierDate   string `json:"supplier_date"`
	PartyAddress   string `json:"party_address"`
	Currency       string `json:"currency"`
	Rate           string `json:"rate"`
	TaxDocumentID  string `json:"tax_document_id"`
	Taxable        string `json:"taxable"`
	Tax            string `json:"tax"`
	Total          string `json:"total"`
	JournalCount   int    `json:"journal_count"`
	Debit          string `json:"debit"`
	Credit         string `json:"credit"`
	LedgerTax      string `json:"ledger_tax"`
}
type AccountantLine struct {
	DocumentID      string                `json:"document_id"`
	Ref             string                `json:"ref"`
	HSN             string                `json:"hsn"`
	Description     string                `json:"description"`
	Quantity        string                `json:"quantity"`
	Unit            string                `json:"unit"`
	TaxRegime       string                `json:"tax_regime"`
	RatePackVersion string                `json:"rate_pack_version"`
	Classification  string                `json:"classification"`
	SupplyType      string                `json:"supply_type"`
	State           string                `json:"state"`
	StateName       string                `json:"state_name"`
	Region          string                `json:"region"`
	ReverseCharge   bool                  `json:"reverse_charge"`
	Taxable         string                `json:"taxable"`
	Tax             string                `json:"tax"`
	Total           string                `json:"total"`
	Components      []AccountantComponent `json:"components"`
}
type AccountantComponent struct {
	Name   string `json:"name"`
	Rate   string `json:"rate"`
	Amount string `json:"amount"`
}
