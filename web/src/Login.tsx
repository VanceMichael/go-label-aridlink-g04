import { LockOutlined, MailOutlined } from "@ant-design/icons";
import { Alert, Button, Form, Input, Typography } from "antd";
import { useState } from "react";
import { api } from "./api";
import type { Session } from "./types";

type LoginProps = { onAuthenticated: (session: Session) => void };

export function Login({ onAuthenticated }: LoginProps) {
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  async function submit(values: { email: string; password: string }) {
    setSubmitting(true);
    setError("");
    try {
      const session = await api.login(values.email, values.password);
      api.setToken(session.token);
      onAuthenticated(session);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Sign in failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-shell">
      <section className="login-panel">
        <div className="brand-lockup">
          <span className="brand-mark">AL</span>
          <div><Typography.Title level={1}>AridLink</Typography.Title><Typography.Text>Joint land restoration operations</Typography.Text></div>
        </div>
        {error && <Alert type="error" message={error} showIcon />}
        <Form layout="vertical" onFinish={submit} requiredMark={false}>
          <Form.Item label="Email" name="email" rules={[{ required: true }, { type: "email" }]}>
            <Input prefix={<MailOutlined />} autoComplete="username" size="large" />
          </Form.Item>
          <Form.Item label="Password" name="password" rules={[{ required: true, min: 10 }]}>
            <Input.Password prefix={<LockOutlined />} autoComplete="current-password" size="large" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={submitting} block size="large">Sign in</Button>
        </Form>
      </section>
      <aside className="login-context">
        <p className="eyebrow">Five-year action network</p>
        <h2>One operational record for evidence, restoration work and partner accountability.</h2>
        <dl><div><dt>Field evidence</dt><dd>Versioned and reviewable</dd></div><div><dt>Program funds</dt><dd>Reserved against accepted milestones</dd></div><div><dt>Regional warnings</dt><dd>Published to affected partners</dd></div></dl>
      </aside>
    </main>
  );
}
