(function () {
  function setupHomepage() {
    const body = document.body;
    if (!body || body.dataset.page !== 'homepage') {
      return;
    }

    const labels = document.querySelectorAll('.container-label');

    labels.forEach((label) => {
      const toggle = (event) => {
        if (event && typeof event.stopPropagation === 'function') {
          event.stopPropagation();
        }

        const isOpen = label.classList.contains('open');
        document.querySelectorAll('.container-label').forEach((other) => {
          if (other !== label) {
            other.classList.remove('open');
          }
        });

        label.classList.toggle('open', !isOpen);
      };

      label.addEventListener('click', toggle);
      label.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          toggle(event);
        }
      });
    });

    if (labels.length) {
      document.addEventListener('click', (event) => {
        document.querySelectorAll('.container-label').forEach((label) => {
          if (!label.contains(event.target)) {
            label.classList.remove('open');
          }
        });
      });
    }

    let connectivityPending = false;
    async function updateConnectivity() {
      if (document.visibilityState === 'hidden' || connectivityPending) {
        return;
      }
      connectivityPending = true;
      try {
        const response = await fetch('/homepage/connectivity', { cache: 'no-store' });
        if (!response.ok) {
          throw new Error(`Connectivity update failed: HTTP ${response.status}`);
        }
        const data = await response.json();
        document.querySelectorAll('[data-connectivity-host]').forEach((cell) => {
          const result = data.results[cell.dataset.connectivityHost];
          cell.textContent = result ? result.summary : 'Not checked';
          cell.title = result ? result.detail : '';
          if (result && result.checkedAt && !result.checkedAt.startsWith('0001-')) {
            cell.title += ` Last checked: ${new Date(result.checkedAt).toLocaleString()}`;
          }
        });
        const banner = document.getElementById('routing-error');
        if (banner) {
          banner.textContent = data.routingError;
          banner.hidden = !data.routingError;
        }
      } catch (error) {
        console.warn('Unable to update connectivity', error);
        document.querySelectorAll('[data-connectivity-host]').forEach((cell) => {
          cell.textContent = 'Status unavailable';
          cell.title = error.message;
        });
      } finally {
        connectivityPending = false;
      }
    }
    updateConnectivity();
    window.addEventListener('focus', updateConnectivity);
    setInterval(updateConnectivity, 5000);

    const revision = body.dataset.currentRevision;
    if (!revision || !/^\d+$/.test(revision)) {
      return;
    }

    let checking = false;
    let reloading = false;
    async function checkForUpdate() {
      if (document.visibilityState === 'hidden' || checking || reloading) {
        return;
      }
      checking = true;
      try {
        const response = await fetch('/homepage/version', { cache: 'no-store' });
        if (!response.ok) {
          throw new Error(`Homepage update check failed: HTTP ${response.status}`);
        }

        const next = (await response.text()).trim();
        if (!/^\d+$/.test(next)) {
          throw new Error('Homepage update check returned an invalid revision');
        }
        if (next !== revision) {
          reloading = true;
          location.reload();
        }
      } catch (error) {
        console.warn('Unable to check for homepage updates', error);
      } finally {
        checking = false;
      }
    }

    window.addEventListener('focus', checkForUpdate);
    window.addEventListener('pageshow', checkForUpdate);
    document.addEventListener('visibilitychange', checkForUpdate);
    setInterval(checkForUpdate, 2000);
  }

  function setupAWSConfig() {
    const form = document.getElementById('aws-form');
    const pasteButton = document.getElementById('paste-and-save-button');
    const manualEntryToggle = document.getElementById('manual-entry-toggle');
    const saveStatus = document.getElementById('aws-save-status');
    if (!form) {
      return;
    }

    function setSaveStatus(message, state) {
      if (saveStatus) {
        saveStatus.textContent = message;
        saveStatus.hidden = !message;
        saveStatus.classList.toggle('is-saving', state === 'saving');
        saveStatus.classList.toggle('is-error', state === 'error');
      }
    }

    if (manualEntryToggle) {
      manualEntryToggle.addEventListener('click', () => {
        const show = form.hidden;
        form.hidden = !show;
        manualEntryToggle.setAttribute('aria-expanded', String(show));
        manualEntryToggle.textContent = show ? 'Hide manual entry' : 'Enter credentials manually';
        if (show) {
          form.querySelector('textarea[name="bootstrapCommand"]')?.focus();
        }
      });
    }
    form.addEventListener('input', () => setSaveStatus('', ''));

    function clearBootstrapCommand() {
      const textarea = form.querySelector('textarea[name="bootstrapCommand"]');
      if (textarea) {
        textarea.value = '';
      }
    }

    function normalizeBootstrapCommandText(value) {
      return (value || '').toString().trim();
    }

    function looksLikeAWSBootstrapCommand(value) {
      const trimmed = normalizeBootstrapCommandText(value);
      return trimmed.toLowerCase().startsWith('aws configure');
    }

    async function submitForm() {
      setSaveStatus('Saving and validating AWS credentials...', 'saving');
      const formData = new FormData(form);
      const bootstrapCommand = normalizeBootstrapCommandText(formData.get('bootstrapCommand'));
      if (bootstrapCommand && !looksLikeAWSBootstrapCommand(bootstrapCommand)) {
        console.warn('[aws-config] rejected bootstrap command that does not start with aws configure', { bootstrapCommandPreview: bootstrapCommand.slice(0, 80) });
        setSaveStatus('Commands must begin with "aws configure".', 'error');
        return false;
      }
      const payload = new URLSearchParams({
        bootstrapCommand,
      });

      try {
        const response = await fetch('/api/aws-config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8' },
          body: payload,
        });

        if (!response.ok) {
          const text = await response.text();
          console.error('[aws-config] save failed', { status: response.status });
          setSaveStatus(text || 'Unable to save AWS credentials.', 'error');
          alert(text || 'failed');
          return false;
        }
      } catch (error) {
        console.error('[aws-config] save request failed', error);
        setSaveStatus('Unable to save AWS credentials. Check the connection and try again.', 'error');
        alert('Unable to save AWS credentials. Check the connection and try again.');
        return false;
      }

      clearBootstrapCommand();
      setSaveStatus('AWS credentials saved and validated successfully.', 'success');
      return true;
    }

    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      await submitForm();
    });

    if (pasteButton) {
      pasteButton.addEventListener('click', async () => {
        setSaveStatus('Saving and validating AWS credentials...', 'saving');
        let text;
        try {
          text = await navigator.clipboard.readText();
        } catch (error) {
          console.error('[aws-config] clipboard read failed', error);
          setSaveStatus('Unable to read clipboard contents.', 'error');
          alert('Unable to read clipboard contents.');
          return;
        }

        const command = normalizeBootstrapCommandText(text);
        if (!command) {
          setSaveStatus('Clipboard is empty. Copy AWS credential commands and try again.', 'error');
          return;
        }
        if (!looksLikeAWSBootstrapCommand(command)) {
          console.warn('[aws-config] clipboard rejected for paste-and-save', { preview: command.slice(0, 80) });
          setSaveStatus('Clipboard contents must begin with "aws configure".', 'error');
          return;
        }

        const textarea = form.querySelector('textarea[name="bootstrapCommand"]');
        if (textarea) {
          textarea.value = command;
        }
        await submitForm();
      });
    }
  }

  function setupAliasForm() {
    const form = document.getElementById('alias-form');
    if (!form) {
      return;
    }

    const source = document.getElementById('alias-source');
    const aliasInput = document.getElementById('alias-value');
    if (source && aliasInput) {
      const updateSuggestions = () => {
        const selected = source.selectedOptions[0];
        const list = selected && selected.dataset.suggestions;
        if (list) {
          aliasInput.setAttribute('list', list);
        } else {
          aliasInput.removeAttribute('list');
        }
      };
      source.addEventListener('change', updateSuggestions);
      updateSuggestions();
    }

    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const formData = new FormData(form);
      const sourceHostname = (formData.get('sourceHostname') || '').toString().trim();
      const alias = (formData.get('alias') || '').toString().trim();
      if (!sourceHostname || !alias) {
        alert('Choose a mapped domain and enter an alias before submitting.');
        return;
      }

      const response = await fetch('/api/aliases', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8' },
        body: new URLSearchParams({ sourceHostname, alias }),
      });

      if (!response.ok) {
        const message = (await response.text()) || 'failed';
        alert(message);
        return;
      }

      location.reload();
    });
  }

  function setupTunnelPage() {
    const form = document.getElementById('tunnel-form');
    setupAliasForm();
    if (form) {
      form.addEventListener('submit', async (event) => {
        event.preventDefault();
        const formData = new FormData(event.target);
        const payload = {
          name: (formData.get('name') || '').toString(),
          profile: (formData.get('profile') || '').toString(),
          region: (formData.get('region') || '').toString(),
          stack: (formData.get('stack') || '').toString(),
          stage: (formData.get('stage') || '').toString(),
          app: (formData.get('app') || '').toString(),
          alb: (formData.get('alb') || '').toString(),
          upstreamProtocol: formData.get('https') === 'on' ? 'https' : 'http',
          verifySSL: formData.get('verifySSL') === 'on',
        };

        const response = await fetch('/api/tunnels', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });

        if (!response.ok) {
          alert((await response.text()) || 'failed');
          return;
        }

        location.reload();
      });
    }

    document.querySelectorAll('[data-delete]').forEach((button) => {
      button.addEventListener('click', async function () {
        const id = this.getAttribute('data-delete');
        const response = await fetch('/api/tunnels/' + encodeURIComponent(id), { method: 'DELETE' });
        if (!response.ok) {
          alert((await response.text()) || 'failed');
          return;
        }

        location.reload();
      });
    });

    document.querySelectorAll('[data-start]').forEach((button) => {
      button.addEventListener('click', async function () {
        const id = this.getAttribute('data-start');
        const response = await fetch('/api/tunnels/start/' + encodeURIComponent(id), {
          method: 'POST',
        });

        if (!response.ok) {
          alert((await response.text()) || 'failed');
          return;
        }

        location.reload();
      });
    });

    document.querySelectorAll('[data-stop]').forEach((button) => {
      button.addEventListener('click', async function () {
        const id = this.getAttribute('data-stop');
        const response = await fetch('/api/tunnels/stop/' + encodeURIComponent(id), {
          method: 'POST',
        });

        if (!response.ok) {
          alert((await response.text()) || 'failed');
          return;
        }

        location.reload();
      });
    });
  }

  function setupCommandGenerator() {
    const form = document.getElementById('command-form');
    const output = document.getElementById('generated-command');
    const copyButton = document.getElementById('copy-button');
    const tunnelFields = document.querySelectorAll('[id^="host-tunnel-fields"]');

    if (!form || !output || !copyButton) {
      return;
    }

    function updateVisibility() {
      const command = form.elements.command.value;
      const show = command === 'host-tunnel';
      tunnelFields.forEach((element) => {
        element.classList.toggle('hidden', !show);
      });

      const secondaryFields = document.querySelectorAll('[data-command-secondary]');
      secondaryFields.forEach((field) => {
        const parent = field.closest('.command-field');
        const isDisabled = command === 'bash';
        if (parent) {
          parent.classList.toggle('is-disabled', isDisabled);
          parent.classList.toggle('is-active', !isDisabled);
        }
        field.disabled = isDisabled;
      });
    }

    function render() {
      const data = new FormData(form);
      const container = (data.get('container') || 'chekov').toString().trim() || 'chekov';
      const user = data.get('user') || 'root';
      const command = (data.get('command') || 'shell').toString().trim();
      const profile = (data.get('profile') || '').toString().trim();
      const stack = (data.get('stack') || '').toString().trim();
      const stage = (data.get('stage') || '').toString().trim();
      const app = (data.get('app') || '').toString().trim();
      const region = (data.get('region') || '').toString().trim();
      const alb = (data.get('alb') || '').toString().trim();
      const localPort = (data.get('localPort') || '').toString().trim();
      const name = (data.get('name') || '').toString().trim();
      const parts = ['docker', 'exec'];

      if (user === 'root') {
        parts.push('-u', 'root');
      }

      if (command === 'bash') {
        parts.push('-it', '-e', `AWS_PROFILE=${profile || 'frontend'}`, '-e', `AWS_REGION=${region || 'eu-west-1'}`, container, 'bash');
      } else {
        parts.push('-it', container, command);

        if (profile) {
          parts.push('--profile', profile);
        }

        if (command === 'host-tunnel') {
          if (stack) parts.push('--stack', stack);
          if (stage) parts.push('--stage', stage);
          if (app) parts.push('--app', app);
          if (region) parts.push('--region', region);
          if (alb) parts.push('--alb', alb);
          if (localPort) parts.push('--local-port', localPort);
          if (name) parts.push('--name', name);
        } else {
          if (stack) parts.push('--stack', stack);
          if (stage) parts.push('--stage', stage);
          if (app) parts.push('--app', app);
          if (region) parts.push('--region', region);
        }
      }

      output.textContent = parts.join(' ');
    }

    form.addEventListener('input', () => {
      updateVisibility();
      render();
    });

    form.addEventListener('change', () => {
      updateVisibility();
      render();
    });

    copyButton.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(output.textContent);
        copyButton.textContent = 'Copied';
        setTimeout(() => {
          copyButton.textContent = 'Copy command';
        }, 1200);
      } catch (_error) {
        copyButton.textContent = 'Copy failed';
      }
    });

    updateVisibility();
    render();
  }

  function setupRegionControls() {
    const controls = document.querySelectorAll('.region-control');
    if (!controls.length) {
      return;
    }

    const predefined = new Set(['eu-west-1', 'us-east-1', 'us-east-2', 'eu-west-2', 'us-west-2']);

    controls.forEach((control) => {
      const select = control.querySelector('.region-select');
      const input = control.querySelector('.region-input');
      if (!select || !input) {
        return;
      }

      const syncSelectFromInput = () => {
        const value = (input.value || '').trim();
        if (!value) {
          select.value = 'eu-west-1';
          return;
        }
        select.value = predefined.has(value) ? value : 'custom';
      };

      const syncInputFromSelect = () => {
        const value = select.value;
        if (value === 'custom') {
          return;
        }
        input.value = value;
      };

      input.addEventListener('input', syncSelectFromInput);
      input.addEventListener('change', syncSelectFromInput);
      select.addEventListener('change', syncInputFromSelect);
      syncSelectFromInput();
    });
  }

  function setupAliasPage() {
    document.querySelectorAll('[data-delete-alias]').forEach((button) => {
      button.addEventListener('click', async function () {
        const alias = this.getAttribute('data-delete-alias');
        if (!alias) {
          return;
        }

        const response = await fetch('/api/aliases/' + encodeURIComponent(alias), {
          method: 'DELETE',
        });

        if (!response.ok) {
          alert((await response.text()) || 'failed');
          return;
        }

        location.reload();
      });
    });
  }


  document.addEventListener('DOMContentLoaded', () => {
    setupRegionControls();

    const page = document.body.dataset.page;

    if (page === 'homepage') {
      setupHomepage();
    }

    if (page === 'aws-config') {
      setupAWSConfig();
    }

    if (page === 'tunnel-page') {
      setupTunnelPage();
    }

    if (page === 'command-generator') {
      setupCommandGenerator();
    }

    if (page === 'alias-page') {
      setupAliasForm();
      setupAliasPage();
    }
  });
})();
