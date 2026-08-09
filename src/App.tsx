import { FormEvent, useEffect, useMemo, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { supportedPlatforms } from "./projectScope";

type Candidate = { id: string | number; content: string; state: "proposed" | "promoted" | "rejected" };
type Knowledge = { id: string | number; content: string };
type KnowledgeListResponse = { result?: { value?: Array<{ knowledge: { id: string }; content: string }> } };

export function App() {
  const [source, setSource] = useState("");
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [knowledge, setKnowledge] = useState<Knowledge[]>([]);
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState("尚未查询。");
	const [coreStatus, setCoreStatus] = useState("桌面 gateway 未连接。");

	useEffect(() => {
		async function connectCore() {
			try {
				const status = await invoke<{ ready: boolean }>("desktop_core_status");
				if (!status.ready) {
					setCoreStatus("Go 核心构件尚未就绪。");
					return;
				}
				await invoke("desktop_core_start");
				const response = await invoke<KnowledgeListResponse>("desktop_knowledge_list");
				const entries = response.result?.value ?? [];
				setKnowledge(entries.map((item) => ({ id: item.knowledge.id, content: item.content })));
				setCoreStatus("Go 核心已由桌面 gateway 启动。");
			} catch {
				setCoreStatus("浏览器预览模式：未连接桌面 gateway。" );
			}
		}
		void connectCore();
	}, []);

  const activeCandidates = useMemo(
    () => candidates.filter((item) => item.state === "proposed"),
    [candidates],
  );

  async function importText(event: FormEvent) {
    event.preventDefault();
    const content = source.trim();
    if (!content) return;
    try {
      const response = await invoke<{ result?: { value?: { candidates?: Array<{ id: string; content: string }> } } }>("desktop_import_text", { content, displayName: "GUI text" });
      const imported = response.result?.value?.candidates ?? [];
      setCandidates(imported.map((item) => ({ id: item.id, content: item.content, state: "proposed" })));
      setSource("");
    } catch {
      setCoreStatus("导入失败：核心不可用或拒绝了请求。");
    }
  }

  function updateCandidate(id: string | number, content: string) {
    setCandidates((items) => items.map((item) => (item.id === id ? { ...item, content } : item)));
  }

  function promote(candidate: Candidate) {
    setCandidates((items) => items.map((item) => (item.id === candidate.id ? { ...item, state: "promoted" } : item)));
    setKnowledge((items) => [...items, { id: candidate.id, content: candidate.content }]);
  }

  function reject(id: string | number) {
    setCandidates((items) => items.map((item) => (item.id === id ? { ...item, state: "rejected" } : item)));
  }

  function ask(event: FormEvent) {
    event.preventDefault();
    const terms = question.toLocaleLowerCase();
    const evidence = knowledge.filter((item) => item.content.toLocaleLowerCase().includes(terms));
    setAnswer(evidence.length ? `个人知识引用：${evidence.map((item) => item.content).join("；")}` : "无法基于个人知识回答。");
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
          <button type="submit">生成候选</button>
        </form>
      </section>
      <section aria-labelledby="candidate-title">
        <h2 id="candidate-title">候选审批</h2>
        {activeCandidates.length === 0 ? <p>暂无待确认候选。</p> : activeCandidates.map((candidate) => (
          <article key={candidate.id} className="card">
            <textarea aria-label="候选内容" value={candidate.content} onChange={(event) => updateCandidate(candidate.id, event.target.value)} />
            <p><button onClick={() => promote(candidate)}>确认入库</button> <button onClick={() => reject(candidate.id)}>拒绝</button></p>
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
          <button type="submit">查询</button>
        </form>
        <output>{answer}</output>
      </section>
      <footer>目标平台：{supportedPlatforms.join("、")}（Debian/Ubuntu Linux）</footer>
    </main>
  );
}
