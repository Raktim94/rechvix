import { ApiError, NetworkError } from "../src/api/client";
import type { SagaApi } from "../src/sync/saga";

interface Doc { ID: string; Status: string; DocumentNumber: string; ref: string; lines: { ID: string; qty: string; price: string }[]; total: string }

/** A stand-in for the Rechvix sales API that behaves like the real one where it matters: lines only on drafts,
 *  finalise assigns the next number and posts exactly once, receipts accumulate. `crashAt` simulates the connection
 *  dying *after the server did the work* (the nastiest case: the phone never hears the answer). */
export class FakeServer implements SagaApi {
  docs = new Map<string, Doc>();
  receipts: { doc: string; amount: string; method: string }[] = [];
  finalizeCount = 0;
  nextNo = 1;
  calls: string[] = [];
  crashAt: string | null = null;
  reject: { path: RegExp; status: number; code: string; message: string } | null = null;

  private maybeCrash(step: string): void {
    if (this.crashAt === step) { this.crashAt = null; throw new NetworkError(); }
  }

  async get<T>(path: string): Promise<T> {
    this.calls.push(`GET ${path}`);
    let m = /^\/sales\/documents\/([^/]+)$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      return { document: { ID: d.ID, Status: d.Status, DocumentNumber: d.Status === "FINALIZED" ? d.DocumentNumber : "", GrandTotalAmount: d.Status === "FINALIZED" ? { amount: d.total, currency: "INR" } : null }, lines: d.lines } as T;
    }
    m = /^\/accounting\/sales-documents\/([^/]+)\/receipts$/.exec(path);
    if (m) return { receipts: this.receipts.filter((r) => r.doc === m![1]).map((r) => ({ Amount: { amount: r.amount, currency: "INR" } })) } as T;
    throw new Error(`fake: unhandled GET ${path}`);
  }

  async post<T>(path: string, body?: unknown): Promise<T> {
    this.calls.push(`POST ${path}`);
    if (this.reject?.path.test(path)) throw new ApiError(this.reject.status, this.reject.code, this.reject.message);
    const b = (body ?? {}) as Record<string, string>;
    if (path === "/sales/documents") {
      const id = `doc-${this.docs.size + 1}`;
      this.docs.set(id, { ID: id, Status: "DRAFT", DocumentNumber: "", ref: b.customer_reference ?? "", lines: [], total: "0" });
      this.maybeCrash("after-create");
      return { ID: id } as T;
    }
    let m = /^\/sales\/documents\/([^/]+)\/lines$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      if (d.Status !== "DRAFT") throw new ApiError(409, "NOT_DRAFT", "Document is not a draft.");
      d.lines.push({ ID: `l${d.lines.length + 1}`, qty: b.quantity!, price: b.unit_price! });
      if (this.crashAt === `after-line-${d.lines.length}`) { this.crashAt = null; throw new NetworkError(); }
      return {} as T;
    }
    m = /^\/sales\/documents\/([^/]+)\/finalize$/.exec(path);
    if (m) {
      const d = this.docs.get(m[1]!)!;
      if (d.Status !== "DRAFT") throw new ApiError(409, "NOT_DRAFT", "Already finalised.");
      d.Status = "FINALIZED";
      d.DocumentNumber = `INV/${String(this.nextNo++).padStart(6, "0")}`;
      const sub = d.lines.reduce((s, l) => s + Number(l.qty) * Number(l.price), 0);
      d.total = (sub * 1.18).toFixed(2); // 18% GST computed by the server, never by the phone
      this.finalizeCount++;
      this.maybeCrash("after-finalize");
      return {} as T;
    }
    if (path === "/accounting/receipts") {
      this.receipts.push({ doc: b.sales_document_id!, amount: b.amount!, method: b.method! });
      this.maybeCrash("after-receipt");
      return {} as T;
    }
    throw new Error(`fake: unhandled POST ${path}`);
  }
}
