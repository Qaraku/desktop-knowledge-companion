import { supportedPlatforms } from "./projectScope";

export function App() {
  return (
    <main className="app-shell">
      <p className="eyebrow">Desktop Knowledge Companion</p>
      <h1>工程基线已就绪</h1>
      <p>TSK-01 仅初始化桌面壳、GUI 和 Python core 的工作区。</p>
      <dl>
        <div>
          <dt>目标平台</dt>
          <dd>{supportedPlatforms.join("、")}（Debian/Ubuntu Linux）</dd>
        </div>
        <div>
          <dt>知识核心</dt>
          <dd>将在 TSK-02 初始化</dd>
        </div>
      </dl>
    </main>
  );
}
