import { LogoutOutlined } from "@ant-design/icons";
import { Button, ConfigProvider, Layout, Typography } from "antd";
import { useState } from "react";
import { api } from "./api";
import { Login } from "./Login";
import { Overview } from "./Overview";
import type { Session } from "./types";

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  async function logout() { try { await api.logout(); } finally { setSession(null); } }
  return <ConfigProvider theme={{token:{colorPrimary:"#167a66",colorInfo:"#167a66",borderRadius:6,fontFamily:"Inter, ui-sans-serif, system-ui"}}}>
    {!session ? <Login onAuthenticated={setSession}/> : <Layout className="app-layout"><Layout.Sider width={248} theme="light" className="sidebar"><div className="brand-lockup compact"><span className="brand-mark">AL</span><Typography.Title level={3}>AridLink</Typography.Title></div><nav><a className="active">Operations</a><a>Monitoring</a><a>Interventions</a><a>Evidence review</a><a>Funding</a><a>Warnings</a></nav><div className="account"><strong>{session.user.email}</strong><span>{session.user.role.replaceAll("_"," ")}</span><Button icon={<LogoutOutlined/>} onClick={logout}>Sign out</Button></div></Layout.Sider><Layout.Content><Overview/></Layout.Content></Layout>}
  </ConfigProvider>;
}
