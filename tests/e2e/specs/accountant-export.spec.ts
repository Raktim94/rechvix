import { test, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

for (const width of [375,768,1440]) {
 test(`Spanish accountant handoff at ${width}px`, async ({page})=>{
  await page.setViewportSize({width,height:1000});
  await page.addInitScript(()=>{localStorage.setItem("rechvix:session-hint",JSON.stringify({organisationId:"org",userId:"user"}));localStorage.setItem("rechvix:language","en");});
  let requested="";
  await page.route("**/api/v1/**",async route=>{
   const url=new URL(route.request().url());const path=url.pathname.replace("/api/v1","");let body:unknown=[];
   if(path==="/organisation")body={ID:"org",Name:"Example Industries",DefaultCurrencyCode:"EUR"};
   if(path==="/legal-entities")body={legal_entities:[{ID:"es-company",LegalName:"Taller Ejemplo S.L.",CountryCode:"ES",BaseCurrencyCode:"EUR",USCollectionRegions:[]}]};
   if(path==="/branches")body={branches:[{ID:"branch",LegalEntityID:"es-company",Name:"Main"}]};
   if(path==="/branches/branch/warehouses")body={warehouses:[{ID:"warehouse",BranchID:"branch",Name:"Main"}]};
   if(path==="/reference/tax")body={supported:true,categories:[],rate_pack_version:"2026"};
   if(path==="/reports/tax/summary")body={title:"Tax summary",headers:["Tax","Amount"],rows:[]};
   if(path==="/reports/tax/accountant-package"){
    requested=url.search;
    body={status:"CA_REVIEW_REQUIRED",document_count:3,snapshot_at:"2026-10-21T00:00:00Z",checks:[{severity:"REVIEW",code:"COMPLETENESS",message:"Confirm all records and deduction eligibility."}],boxes:[{code:"09",label:"IVA 21%",amount:"210.00",status:"CALCULATED_REVIEW",note:""},{code:"29",label:"Deductible IVA",amount:"",status:"CA_INPUT_REQUIRED",note:""}]};
   }
   await route.fulfill({json:body});
  });
  await page.goto("/gst");
  await page.getByLabel("Period start",{exact:true}).fill("2026-07-01");
  await page.getByLabel("Period end",{exact:true}).fill("2026-09-30");
  await page.getByRole("button",{name:"Check accountant package"}).click();
  await expect(page.getByRole("link",{name:"Download CA package"})).toHaveAttribute("href",/format=zip/);
  expect(requested).toContain("legal_entity_id=es-company");expect(requested).toContain("from=2026-07-01");expect(requested).toContain("to=2026-09-30");
  await page.getByText("Calculated filing worksheet",{exact:true}).click();
  await expect(page.getByRole("cell",{name:"CA input required",exact:true})).toBeVisible();
  const results=await new AxeBuilder({page}).include("section[aria-labelledby='accountant-export-heading']").analyze();
  expect(results.violations.filter(v=>v.impact==="serious"||v.impact==="critical")).toEqual([]);
  for (const action of [page.getByRole("button",{name:"Check accountant package"}),page.getByRole("link",{name:"Download CA package"}),page.getByRole("link",{name:"Workbook only"})]) {
   const box=await action.boundingBox();
   expect(box).not.toBeNull();
   expect(box!.x).toBeGreaterThanOrEqual(0);
   expect(box!.x+box!.width).toBeLessThanOrEqual(width);
  }
  await page.screenshot({path:`test-results/accountant-export-${width}.png`,fullPage:true});
  await page.getByLabel("Period end",{exact:true}).fill("2026-10-31");
  await expect(page.getByRole("link",{name:"Download CA package"})).toHaveCount(0);
 });
}
