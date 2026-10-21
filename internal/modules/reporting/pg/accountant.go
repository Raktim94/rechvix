package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"rechvix/internal/modules/reporting/domain"
)

// AccountantData uses one statement/MVCC snapshot for metadata, source registers,
// tax lines, components and journal controls. Explicit company + tenant predicates
// complement RLS. Components are nested, so bases are never multiplied by tax count.
func (r *Repo) AccountantData(ctx context.Context, f domain.Filter) (*domain.AccountantData, error) {
	if f.LegalEntityID == nil || f.From == nil || f.To == nil {
		return nil, fmt.Errorf("accountant export requires company and dates")
	}
	const query = `WITH company AS (
 SELECT le.id,le.legal_name AS name,COALESCE(NULLIF(le.country_code,''),'IN') AS country,
 o.default_currency_code AS currency,CASE WHEN COALESCE(le.country_code,'IN')='IN' THEN COALESCE(le.gstin,'') ELSE COALESCE(le.tax_id,'') END AS tax_id,
 COALESCE(le.gst_state_code,'') AS state,le.us_collection_regions FROM legal_entities le JOIN organisations o ON o.id=le.organisation_id
 WHERE le.organisation_id=$1 AND le.id=$2
 ), docs AS (
 SELECT sd.id,'OUTPUT' AS direction,sd.document_type AS type,sd.document_number AS number,sd.issue_date AS date,
 sd.supply_date,sd.status,p.legal_name AS party,COALESCE(tr.registration_number,'') AS party_tax_id,
 COALESCE(ref.document_number,'') AS reference,''::text AS supplier_number,''::text AS supplier_date,COALESCE((SELECT concat_ws(' | ',line1,line2,city,state,postal_code,country_code) FROM party_addresses WHERE id=COALESCE(sd.shipping_address_id,sd.billing_address_id)),'') AS party_address,sd.currency_code AS currency,
 sd.exchange_rate AS rate,sd.tax_document_id, 'sales_document'::text AS source_type
 FROM sales_documents sd JOIN company c ON c.id=sd.legal_entity_id JOIN parties p ON p.id=sd.customer_party_id
 LEFT JOIN party_tax_registrations tr ON tr.id=sd.customer_tax_registration_id
 LEFT JOIN sales_documents ref ON ref.id=sd.reference_document_id
 WHERE sd.organisation_id=$1 AND sd.issue_date >= $3 AND sd.issue_date <= $4
 AND sd.document_type IN ('TAX_INVOICE','POS_INVOICE','RECURRING_INVOICE','CREDIT_NOTE','DEBIT_NOTE','SALES_RETURN')
 UNION ALL
 SELECT pd.id,'INPUT',pd.document_type,pd.document_number,pd.document_date,NULL::date,pd.status,p.legal_name,
 COALESCE((SELECT registration_number FROM party_tax_registrations WHERE party_id=p.id ORDER BY is_primary DESC,id LIMIT 1),''),
 COALESCE(ref.document_number,''),COALESCE(pd.supplier_invoice_number,''),COALESCE(pd.supplier_invoice_date::text,''),COALESCE((SELECT concat_ws(' | ',line1,line2,city,state,postal_code,country_code) FROM party_addresses WHERE party_id=p.id ORDER BY is_default DESC,id LIMIT 1),''),pd.currency_code,1::numeric,pd.tax_document_id,'purchase_document'
 FROM purchase_documents pd JOIN branches b ON b.id=pd.branch_id JOIN company c ON c.id=b.legal_entity_id
 JOIN parties p ON p.id=pd.supplier_party_id LEFT JOIN purchase_documents ref ON ref.id=pd.reference_document_id
 WHERE pd.organisation_id=$1 AND pd.document_date >= $3 AND pd.document_date <= $4
 AND pd.document_type IN ('PURCHASE_INVOICE','PURCHASE_RETURN','DEBIT_NOTE')
 ), registers AS (
 SELECT d.id,d.direction,d.type,d.number,d.date::text,COALESCE(d.supply_date::text,'') AS supply_date,d.status,d.party,d.party_tax_id,d.reference,d.supplier_number,d.supplier_date,d.party_address,d.currency,d.rate::text,
 COALESCE(d.tax_document_id::text,'') AS tax_document_id,COALESCE(td.total_taxable_amount::text,'') AS taxable,COALESCE(td.total_tax_amount::text,'') AS tax,COALESCE(td.grand_total,CASE WHEN d.direction='OUTPUT' THEN (SELECT COALESCE(SUM(line_total_amount),0) FROM sales_document_lines WHERE sales_document_id=d.id) ELSE (SELECT COALESCE(SUM(line_total_amount),0) FROM purchase_document_lines WHERE purchase_document_id=d.id) END)::text AS total,
 (SELECT COUNT(*) FROM journals j WHERE j.organisation_id=$1 AND j.source_type=d.source_type AND j.source_id=d.id)::int AS journal_count,
 COALESCE((SELECT SUM(jl.debit_amount) FROM journals j JOIN journal_lines jl ON jl.journal_id=j.id WHERE j.organisation_id=$1 AND j.source_type=d.source_type AND j.source_id=d.id),0)::text AS debit,
 COALESCE((SELECT SUM(jl.credit_amount) FROM journals j JOIN journal_lines jl ON jl.journal_id=j.id WHERE j.organisation_id=$1 AND j.source_type=d.source_type AND j.source_id=d.id),0)::text AS credit,
 COALESCE((SELECT SUM(CASE WHEN d.direction='OUTPUT' THEN jl.credit_amount-jl.debit_amount ELSE jl.debit_amount-jl.credit_amount END)
 FROM journals j JOIN journal_lines jl ON jl.journal_id=j.id JOIN accounts a ON a.id=jl.account_id
 WHERE j.organisation_id=$1 AND j.source_type=d.source_type AND j.source_id=d.id AND a.code=CASE WHEN d.direction='OUTPUT' THEN '2100' ELSE '1400' END),0)::text AS ledger_tax
 FROM docs d LEFT JOIN tax_documents td ON td.id=d.tax_document_id ORDER BY d.date,d.direction,d.number,d.id LIMIT 20001
 ), source_lines AS (
 SELECT d.id AS document_id,tl.line_ref AS ref,tl.hsn_sac_code AS hsn,COALESCE(pr.name,'') AS description,
 COALESCE(sl.quantity,pl.quantity,0)::text AS quantity,COALESCE(u.code,'') AS unit,COALESCE(tl.tax_regime,'GST_IN') AS tax_regime,COALESCE(tl.rate_pack_version,tl.tax_rate_master_id::text,'') AS rate_pack_version,tl.classification,td.supply_type,
 COALESCE(td.place_of_supply_code,'') AS state,COALESCE(gs.name,'') AS state_name,COALESCE(td.supply_region,'') AS region,td.reverse_charge,
 tl.taxable_amount::text AS taxable,tl.total_tax_amount::text AS tax,tl.gross_amount::text AS total,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('name',tc.component_type,'rate',tc.rate::text,'amount',tc.amount::text) ORDER BY tc.component_type,tc.id) FROM tax_components tc WHERE tc.tax_line_id=tl.id),'[]'::jsonb) AS components
 FROM docs d JOIN tax_documents td ON td.id=d.tax_document_id JOIN tax_lines tl ON tl.tax_document_id=td.id
 LEFT JOIN sales_document_lines sl ON d.direction='OUTPUT' AND sl.sales_document_id=d.id AND sl.line_number::text=tl.line_ref
 LEFT JOIN purchase_document_lines pl ON d.direction='INPUT' AND pl.purchase_document_id=d.id AND pl.line_number::text=tl.line_ref
 LEFT JOIN product_variants pv ON pv.id=COALESCE(sl.product_variant_id,pl.product_variant_id)
 LEFT JOIN products pr ON pr.id=pv.product_id LEFT JOIN units_of_measure u ON u.id=COALESCE(sl.unit_id,pl.unit_id)
 LEFT JOIN gst_state_codes gs ON gs.code=td.place_of_supply_code
 WHERE d.status='FINALIZED' ORDER BY d.date,d.id,tl.line_ref LIMIT 100001
 ) SELECT jsonb_build_object('company',to_jsonb(c),'snapshot_at',statement_timestamp(),
 'documents',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY date,direction,number,id) FROM registers r),'[]'::jsonb),
 'lines',COALESCE((SELECT jsonb_agg(to_jsonb(l) ORDER BY document_id,ref) FROM source_lines l),'[]'::jsonb)) FROM company c`
	var raw []byte
	if err := r.pool.Q(ctx).QueryRow(ctx, query, f.OrganisationID, *f.LegalEntityID, *f.From, *f.To).Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrAccountantCompanyNotFound
		}
		return nil, fmt.Errorf("accountant package query: %w", err)
	}
	var data domain.AccountantData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("accountant package decode: %w", err)
	}
	return &data, nil
}
