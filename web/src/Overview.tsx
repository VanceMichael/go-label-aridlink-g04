import { AlertOutlined, BankOutlined, EnvironmentOutlined, ExperimentOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Descriptions, Empty, Flex, Input, Progress, Segmented, Space, Spin, Table, Tag, Typography } from "antd";
import { useEffect, useMemo, useState } from "react";
import { api } from "./api";
import type { ProgramOverview, Site } from "./types";

export function Overview() {
  const [programId, setProgramId] = useState(localStorage.getItem("aridlink-program") ?? "");
  const [mode, setMode] = useState<string>("Program");
  const [overview, setOverview] = useState<ProgramOverview | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    if (!programId.trim()) return;
    setLoading(true); setError("");
    try {
      const [summary, siteList] = await Promise.all([api.overview(programId.trim()), api.sites(programId.trim())]);
      setOverview(summary); setSites(siteList.items); localStorage.setItem("aridlink-program", programId.trim());
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Could not load program"); }
    finally { setLoading(false); }
  }

  useEffect(() => { if (programId) void load(); }, []);
  const spentPercent = useMemo(() => overview && overview.budget_cents > 0 ? Math.round(overview.disbursed_cents / overview.budget_cents * 100) : 0, [overview]);
  const money = (cents: number) => new Intl.NumberFormat("en", { style: "currency", currency: "USD", maximumFractionDigits: 0 }).format(cents / 100);

  return <div className="workspace">
    <header className="workspace-header"><div><p className="eyebrow">Regional operations</p><Typography.Title level={2}>Action program control room</Typography.Title></div><Space.Compact><Input value={programId} onChange={e=>setProgramId(e.target.value)} placeholder="Program ID" onPressEnter={load}/><Button icon={<ReloadOutlined/>} onClick={load} loading={loading}>Load</Button></Space.Compact></header>
    {error && <div className="inline-error">{error}</div>}
    <Segmented value={mode} onChange={value=>setMode(String(value))} options={["Program","Sites","Monitoring","Evidence"]}/>
    {loading ? <div className="loading"><Spin size="large"/></div> : !overview ? <Empty description="Select a program to begin"/> : <>
      <section className="status-strip">
        <div><BankOutlined/><span>Program</span><strong>{overview.program_name}</strong><Tag color="green">{overview.program_status}</Tag></div>
        <div><EnvironmentOutlined/><span>Sites</span><strong>{Object.values(overview.site_states).reduce((a,b)=>a+b,0)}</strong><small>{Object.entries(overview.site_states).map(([k,v])=>`${v} ${k}`).join(" · ")||"No sites"}</small></div>
        <div><ExperimentOutlined/><span>Pending reviews</span><strong>{overview.pending_review_count}</strong><small>{Object.entries(overview.evidence_states).map(([k,v])=>`${v} ${k}`).join(" · ")||"No evidence"}</small></div>
        <div><AlertOutlined/><span>Published alerts</span><strong>{overview.published_alert_count}</strong><small>Partner acknowledgements tracked per organization</small></div>
      </section>
      {mode === "Program" && <section className="program-band"><div><Typography.Title level={3}>Funding position</Typography.Title><Progress type="dashboard" percent={spentPercent} strokeColor="#167a66"/><p>{money(overview.disbursed_cents)} disbursed of {money(overview.budget_cents)}</p></div><Descriptions column={1} bordered size="small" items={[{key:"held",label:"Held budget",children:money(overview.held_budget_cents)},{key:"campaigns",label:"Monitoring",children:Object.entries(overview.campaign_states).map(([k,v])=><Tag key={k}>{v} {k}</Tag>)},{key:"work",label:"Work orders",children:Object.entries(overview.work_order_states).map(([k,v])=><Tag key={k}>{v} {k}</Tag>)},{key:"milestones",label:"Milestones",children:Object.entries(overview.milestone_states).map(([k,v])=><Tag key={k}>{v} {k}</Tag>)}]}/></section>}
      {mode === "Sites" && <Table rowKey="id" dataSource={sites} pagination={false} columns={[{title:"Site",dataIndex:"name"},{title:"Country",dataIndex:"country_code"},{title:"Ecosystem",dataIndex:"ecosystem"},{title:"Area (ha)",dataIndex:"area_hectares"},{title:"Status",dataIndex:"status",render:value=><Tag color={value==="active"?"green":"default"}>{value}</Tag>}]} />}
      {mode === "Monitoring" && <StateBoard title="Monitoring campaign lifecycle" states={overview.campaign_states}/>} 
      {mode === "Evidence" && <StateBoard title="Evidence and review lifecycle" states={overview.evidence_states}/>} 
    </>}
  </div>;
}

function StateBoard({title,states}:{title:string;states:Record<string,number>}) { return <section className="state-board"><Typography.Title level={3}>{title}</Typography.Title><Flex gap={12} wrap>{Object.entries(states).map(([state,count])=><div className="state-cell" key={state}><span>{state}</span><strong>{count}</strong></div>)}</Flex></section>; }
