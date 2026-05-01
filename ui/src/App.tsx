export default function App() {
  return (
    <main className="app-shell">
      <section className="hero">
        <p className="eyebrow">Vigil</p>
        <h1>Local-first observability for side projects.</h1>
        <p className="body-copy">
          This scaffold gives you the monorepo foundation: Go backend, Vite UI,
          runtime data layout, and a clean place to start building logs, traces,
          and lightweight stats.
        </p>
      </section>

      <section className="panel-grid">
        <article className="panel">
          <h2>Backend</h2>
          <p>Go service at <code>/cmd/vigil</code> with a health endpoint at <code>/api/health</code>.</p>
        </article>
        <article className="panel">
          <h2>Frontend</h2>
          <p>React + TypeScript + Vite app that builds into <code>web/dist</code>.</p>
        </article>
        <article className="panel">
          <h2>Storage</h2>
          <p>Project-first raw event layout under <code>vigil-data/logs</code> and SQLite under <code>vigil-data/index</code>.</p>
        </article>
      </section>
    </main>
  );
}

