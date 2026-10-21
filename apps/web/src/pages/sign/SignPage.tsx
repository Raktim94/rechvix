import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError, apiUrl } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface Public { document_type: string; status: string; requested_name?: string; expires_at: string; signed_at?: string; signer_name?: string; consent: string }

/** The page a customer opens from an emailed link: read the document, type a name, optionally draw a signature, agree, sign. */
export function SignPage({ token }: { token: string }) {
  const meta = useQuery({ queryKey: ["sign", token], queryFn: () => api.get<Public>(`/sign/${token}`), retry: false });
  const [name, setName] = useState("");
  const [agree, setAgree] = useState(false);
  const [reason, setReason] = useState("");
  const [declining, setDeclining] = useState(false);
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const drawing = useRef(false);
  const [drawn, setDrawn] = useState(false);

  useEffect(() => {
    if (meta.data?.requested_name) setName((n) => n || meta.data!.requested_name!);
  }, [meta.data]);

  const point = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const c = canvas.current!;
    const r = c.getBoundingClientRect();
    return { x: ((e.clientX - r.left) * c.width) / r.width, y: ((e.clientY - r.top) * c.height) / r.height };
  };
  const down = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const ctx = canvas.current!.getContext("2d")!;
    drawing.current = true;
    canvas.current!.setPointerCapture(e.pointerId);
    const p = point(e);
    ctx.beginPath();
    ctx.moveTo(p.x, p.y);
  };
  const move = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!drawing.current) return;
    const ctx = canvas.current!.getContext("2d")!;
    ctx.lineWidth = 3;
    ctx.lineCap = "round";
    ctx.strokeStyle = "#111";
    const p = point(e);
    ctx.lineTo(p.x, p.y);
    ctx.stroke();
    setDrawn(true);
  };
  const up = () => { drawing.current = false; };
  const clear = () => {
    const c = canvas.current!;
    c.getContext("2d")!.clearRect(0, 0, c.width, c.height);
    setDrawn(false);
  };

  const submit = useMutation({
    mutationFn: (decline: boolean) => api.post<Public>(`/sign/${token}`, {
      name, agree, decline, reason, drawing: !decline && drawn ? canvas.current!.toDataURL("image/png") : "",
    }),
    onSuccess: (d) => { meta.refetch(); void d; },
  });

  if (meta.isPending) return <main className={layout.page}><p>Loading…</p></main>;
  if (meta.isError || !meta.data) {
    return <main className={layout.page}><h1>This link cannot be used</h1><p>It may have expired, been withdrawn or already been used. Ask the sender for a new one.</p></main>;
  }
  const m = meta.data;
  const closed = m.status === "SIGNED" || m.status === "DECLINED";
  return (
    <main className={layout.page} style={{ maxWidth: 900, margin: "0 auto" }}>
      <h1>{m.status === "SIGNED" ? "Document signed" : m.status === "DECLINED" ? "Document declined" : "Please review and sign"}</h1>
      {m.status === "SIGNED" ? <p role="status">Signed by <strong>{m.signer_name}</strong> on {m.signed_at ? new Date(m.signed_at).toLocaleString() : ""}. Thank you. You do not need to do anything else.</p> : null}
      {m.status === "DECLINED" ? <p role="status">You declined this document. The sender has been told.</p> : null}
      <p><a href={apiUrl(`/sign/${token}/document`)} target="_blank" rel="noreferrer">Open the document in a new tab<span className="srOnly"> (opens in a new tab)</span></a></p>
      <iframe title="The document to sign" src={apiUrl(`/sign/${token}/document`)} style={{ width: "100%", height: "70vh", border: "1px solid var(--color-border, #ccc)", borderRadius: 8 }} />
      {!closed ? (
        <section className={layout.panel} aria-labelledby="sign-heading" style={{ marginTop: 16 }}>
          <h2 id="sign-heading">Sign</h2>
          <form onSubmit={(e) => { e.preventDefault(); submit.mutate(false); }}>
            <div className={ui.field}>
              <label htmlFor="sign-name">Type your full name</label>
              <input id="sign-name" className={ui.input} value={name} autoComplete="name" onChange={(e) => setName(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="sign-pad">Draw your signature (optional)</label>
              <canvas id="sign-pad" ref={canvas} width={600} height={180} onPointerDown={down} onPointerMove={move} onPointerUp={up} onPointerLeave={up}
                style={{ width: "100%", maxWidth: 600, height: 180, border: "1px dashed #888", borderRadius: 8, background: "#fff", touchAction: "none" }} aria-label="Signature drawing area" />
              <div><button type="button" className={ui.btnGhost} onClick={clear}>Clear drawing</button></div>
            </div>
            <label style={{ display: "flex", gap: 8, alignItems: "flex-start", margin: "8px 0" }}>
              <input type="checkbox" checked={agree} onChange={(e) => setAgree(e.target.checked)} style={{ marginTop: 4 }} /> <span>{m.consent}</span>
            </label>
            <div className={ui.formActions} style={{ justifyContent: "flex-start", gap: 8, flexWrap: "wrap" }}>
              <button type="submit" className={ui.btnPrimary} disabled={name.trim().length < 2 || !agree || submit.isPending}>{submit.isPending ? "Signing…" : "Sign the document"}</button>
              <button type="button" className={ui.btnSecondary} onClick={() => setDeclining(!declining)}>I do not agree</button>
            </div>
            {declining ? (
              <div className={ui.field} style={{ marginTop: 8 }}>
                <label htmlFor="sign-reason">Why (optional)</label>
                <input id="sign-reason" className={ui.input} value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
                <button type="button" className={ui.btnSecondary} disabled={submit.isPending} onClick={() => submit.mutate(true)}>Decline the document</button>
              </div>
            ) : null}
            {submit.isError ? <p role="alert">{submit.error instanceof ApiError ? submit.error.message : "Could not record your answer."}</p> : null}
          </form>
          <p className={ui.muted}>Your name, the time, your network address and your browser are recorded with your signature as evidence. This is a simple electronic signature.</p>
        </section>
      ) : null}
    </main>
  );
}
