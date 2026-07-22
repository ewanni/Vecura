<template>
  <div class="help">
    <h3>How to use Vecura</h3>
    <ol>
      <li>Open <b>Settings</b> and pick a provider (OpenAI, OpenRouter, …).</li>
      <li>Enter your <b>API Key</b> and press <b>Check</b>. Valid keys load the available embedding models.</li>
      <li>Select exactly one model (radio) and press <b>Use selected model</b>.</li>
      <li>Press <b>Choose…</b> to pick a folder of images to index. Scanning embeds and stores them locally.</li>
      <li>Type a query in the search bar at the top and press Enter. Use ↑/↓ recent suggestions.</li>
      <li>Click any result to preview it; navigate with ‹ / › or arrow keys.</li>
      <li>Toggle the theme with the round button at the bottom of the sidebar.</li>
    </ol>
    <el-alert type="info" :closable="false" title="Privacy">
      Images and embeddings are stored only in <code>~/.vecura/</code>. API keys are used solely to call your chosen provider.
    </el-alert>
  </div>
</template>

<script setup></script>

<style scoped>
.help { padding: 2px; line-height: 1.55; color: var(--fg-2); }
.help h3 {
  margin: 2px 0 14px;
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--fg);
  letter-spacing: -0.01em;
}
.help ol { padding-left: 20px; margin: 0 0 16px; }
.help li { margin-bottom: 10px; font-size: 0.88rem; }
.help li::marker { color: var(--fg-3); font-weight: 600; }
.help b { color: var(--fg); font-weight: 600; }
.help :deep(.el-alert) { font-size: 0.84rem; }
.help :deep(.el-alert__title) { font-size: 0.84rem; }
.help code {
  background: var(--field-bg);
  padding: 1px 5px;
  border-radius: 5px;
  font-size: 0.82em;
  color: var(--fg);
}
</style>
