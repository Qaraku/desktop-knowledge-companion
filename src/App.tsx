import { ChangeEvent, FormEvent, useEffect, useMemo, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { supportedPlatforms } from "./projectScope";

type Candidate = { id: string | number; content: string; state: "proposed" | "editing" | "promoted" | "rejected"; version: number };
type Knowledge = { id: string | number; content: string };
type KnowledgeListResponse = { result?: { value?: Array<{ knowledge: { id: string }; content: string }> } };
type PendingCandidateResponse = { result?: { value?: Candidate[] } };
type QueryRun = { id: string; answer?: string; citations?: Array<{ excerpt: string }>; knowledge_version: number; profile_version: string; trace?: Array<{ sequence: number; stage: string; payload: string }> };
type AnswerView = "concise" | "citations" | "detailed";

export function App() {
  const [source, setSource] = useState("");
	const [displayName, setDisplayName] = useState("GUI text");
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [knowledge, setKnowledge] = useState<Knowledge[]>([]);
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState("尚未查询。");
	const [citations, setCitations] = useState<string[]>([]);
	const [answerView, setAnswerView] = useState<AnswerView>("citations");
	const [queryRun, setQueryRun] = useState<QueryRun | null>(null);
	const [coreStatus, setCoreStatus] = useState("桌面 gateway 未连接。");

	async function refreshKnowledge() {
		const response = await invoke<KnowledgeListResponse>("desktop_knowledge_list");
		setKnowledge((response.result?.value ?? []).map((item) => ({ id: item.knowledge.id, content: item.content })));
	}

	async function refreshPendingCandidates() {
		const response = await invoke<PendingCandidateResponse>("desktop_pending_candidate_list");
		setCandidates(response.result?.value ?? []);
	}

	useEffect(() => {
		async function connectCore() {
			try {
				const status = await invoke<{ ready: boolean }>("desktop_core_status");
				if (!status.ready) {
					setCoreStatus("Go 核心构件尚未就绪。");
					return;
				}
				await invoke("desktop_core_start");
				await Promise.all([refreshKnowledge(), refreshPendingCandidates()]);
				setCoreStatus("Go 核心已由桌面 gateway 启动。");
			} catch {
				setCoreStatus("浏览器预览模式：未连接桌面 gateway。" );
			}
		}
		void connectCore();
	}, []);

  const activeCandidates = useMemo(
    () => candidates.filter((item) => item.state === "proposed" || item.state === "editing"),
    [candidates],
  );

  async function importText(event: FormEvent) {
    event.preventDefault();
    const content = source.trim();
    if (!content) return;
    try {
      const response = await invoke<{ result?: { value?: { candidates?: Array<{ id: string; content: string; version: number }> } } }>("desktop_import_text", { content, displayName });
      const imported = response.result?.value?.candidates ?? [];
      setCandidates((items) => [...items, ...imported.map((item) => ({ id: item.id, content: item.content, state: "proposed" as const, version: item.version }))]);
      setSource("");
		setDisplayName("GUI text");
    } catch {
		await refreshPendingCandidates().catch(() => undefined);
      setCoreStatus("导入失败：核心不可用或拒绝了请求。");
    }
  }

  async function selectMarkdown(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    try {
      setSource(await file.text());
      setDisplayName(file.name);
    } catch {
      setCoreStatus("读取所选 Markdown 文件失败。");
    }
  }

  function updateCandidate(id: string | number, content: string) {
    setCandidates((items) => items.map((item) => (item.id === id ? { ...item, content } : item)));
  }

  async function saveCandidate(candidate: Candidate, content: string) {
    if (content === candidate.content) return;
    try {
      const response = await invoke<{ result?: { value?: Candidate } }>("desktop_update_candidate", {
        candidateId: String(candidate.id),
        expectedVersion: candidate.version,
        content,
      });
      const saved = response.result?.value;
      if (!saved) throw new Error("missing candidate response");
      setCandidates((items) => items.map((item) => (item.id === candidate.id ? saved : item)));
    } catch {
      setCandidates((items) => items.map((item) => (item.id === candidate.id ? candidate : item)));
      setCoreStatus("保存候选失败：候选已变更或核心请求被拒绝。");
    }
  }

  async function promote(candidate: Candidate) {
    try {
      await invoke("desktop_promote_candidate", { candidateId: String(candidate.id) });
      await Promise.all([refreshKnowledge(), refreshPendingCandidates()]);
    } catch {
		await Promise.all([refreshKnowledge().catch(() => undefined), refreshPendingCandidates().catch(() => undefined)]);
      setCoreStatus("确认入库失败：审批或核心请求被拒绝。");
    }
  }

  async function reject(candidate: Candidate) {
    try {
      await invoke("desktop_reject_candidate", { candidateId: String(candidate.id), expectedVersion: candidate.version });
      await refreshPendingCandidates();
    } catch {
		await refreshPendingCandidates().catch(() => undefined);
      setCoreStatus("拒绝候选失败：候选已变更或核心请求被拒绝。");
    }
  }

  async function ask(event: FormEvent) {
    event.preventDefault();
    const trimmedQuestion = question.trim();
    if (!trimmedQuestion) return;
    try {
      const response = await invoke<{ result?: { value?: QueryRun } }>("desktop_query", { question: trimmedQuestion });
      const run = response.result?.value;
      if (!run) throw new Error("missing query response");
      setAnswer(run.answer || "无法基于个人知识回答。");
      setCitations((run.citations ?? []).map((citation) => citation.excerpt));
		setQueryRun(run);
    } catch {
      setCitations([]);
		setQueryRun(null);
      setAnswer("查询失败：核心不可用或拒绝了请求。");
    }
  }

  return (
    <main className="app-shell">
      <header>
        <p className="eyebrow">Desktop Knowledge Companion</p>
        <h1>本地知识工作区</h1>
        <p>核心运行于 Go sidecar；当前界面调用边界由 Tauri gateway 承担。</p>
		<p role="status">{coreStatus}</p>
      </header>
      <section aria-labelledby="import-title">
        <h2 id="import-title">导入</h2>
        <form onSubmit={importText}>
          <label>
            文本或 Markdown
            <textarea value={source} onChange={(event) => setSource(event.target.value)} placeholder="粘贴需要整理的内容" />
          </label>
          <label>
            选择 Markdown 文件
            <input type="file" accept=".md,.markdown,text/markdown,text/plain" onChange={selectMarkdown} />
          </label>
          <button type="submit">生成候选</button>
        </form>
      </section>
      <section aria-labelledby="candidate-title">
        <h2 id="candidate-title">候选审批</h2>
        {activeCandidates.length === 0 ? <p>暂无待确认候选。</p> : activeCandidates.map((candidate) => (
          <article key={candidate.id} className="card">
            <textarea aria-label="候选内容" value={candidate.content} onChange={(event) => updateCandidate(candidate.id, event.target.value)} onBlur={(event) => void saveCandidate(candidate, event.target.value)} />
            <p><button onClick={() => promote(candidate)}>确认入库</button> <button onClick={() => reject(candidate)}>拒绝</button></p>
          </article>
        ))}
      </section>
      <section aria-labelledby="knowledge-title">
        <h2 id="knowledge-title">正式知识</h2>
        {knowledge.length === 0 ? <p>尚无已确认知识。</p> : <ul>{knowledge.map((item) => <li key={item.id}>{item.content}</li>)}</ul>}
      </section>
      <section aria-labelledby="query-title">
        <h2 id="query-title">严格问答</h2>
        <form onSubmit={ask}>
          <label>问题 <input value={question} onChange={(event) => setQuestion(event.target.value)} /></label>
			<label>
				展示层级
				<select value={answerView} onChange={(event) => setAnswerView(event.target.value as AnswerView)}>
					<option value="concise">简洁</option>
					<option value="citations">引用</option>
					<option value="detailed">详细观察</option>
				</select>
			</label>
          <button type="submit">查询</button>
        </form>
        <output>{answer}</output>
		{answerView !== "concise" && citations.length > 0 && <ol aria-label="个人知识引用">{citations.map((citation, index) => <li key={`${index}-${citation}`}>{citation}</li>)}</ol>}
		{answerView === "detailed" && queryRun && <details open>
			<summary>本次运行观察</summary>
			<dl>
				<div><dt>运行 ID</dt><dd>{queryRun.id}</dd></div>
				<div><dt>来源边界</dt><dd>仅本地个人知识；未配置网络或模型来源。</dd></div>
				<div><dt>知识版本 / Profile</dt><dd>{queryRun.knowledge_version} / {queryRun.profile_version}</dd></div>
				<div><dt>检索轨迹</dt><dd>{(queryRun.trace ?? []).map((trace) => `${trace.sequence}. ${trace.stage} ${trace.payload}`).join("；") || "无"}</dd></div>
			</dl>
		</details>}
      </section>
      <footer>目标平台：{supportedPlatforms.join("、")}（Debian/Ubuntu Linux）</footer>
    </main>
  );
}
