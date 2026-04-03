import './style.css';
import { LogicalSize } from '@tauri-apps/api/dpi';
import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { getCurrentWindow } from '@tauri-apps/api/window';

const CHAT_SIZE = { width: 408, height: 700 };
const MENU_SIZE = { width: 352, height: 438 };
const STATUS_SIZE = { width: 380, height: 540 };
const PREVIEW_SIZE = { width: 332, height: 252 };
const ORB_SIZE = { width: 96, height: 96 };
const LONG_PRESS_MS = 420;
const DRAG_THRESHOLD = 14;
const HIDE_PRIME_Y = 18;
const HIDE_SWIPE_Y = 58;
const HIDE_SWIPE_X = 54;
const HIDE_ANIMATION_MS = 260;
const HOVER_PREDICT_MS = 3000;
const STREAM_TICK_MS = 36;
const AGENT_MODES = [
  {
    id: 'work',
    label: '工作',
    orbLabel: 'focus agent',
    title: '工作代理在线',
    copy: '滚轮切换工作/生活/创作；现在更偏任务推进、专注和执行。',
    voice: '工作模式已就绪，适合安排任务和快速执行。',
    voiceStart: '工作模式收音中，优先提炼任务、截止时间和下一步。',
    voiceListening: '继续按住说话；我会把语音整理成待办和执行指令。',
    voiceDone: '已停止收音，松开后会把内容整理成可执行事项。',
    chatTitle: '工作模式会更像你的执行搭档。',
    chatCopy: '它会优先整理待办、聚焦优先级，并把语音内容收成可执行事项。',
    hint: '滚动切换工作/生活/创作形态；当前更偏执行和专注。',
    menuTip: '工作模式下，顺时针画圈打开效率卡组；逆时针收回。',
    menuItems: [
      { title: '冲刺', copy: '压缩任务到 25 分钟番茄节奏' },
      { title: '拆解', copy: '把目标拆成下一步动作' },
      { title: '置顶', copy: '保持前景悬浮，随时盯任务' },
      { title: '贴边', copy: '恢复不挡操作的贴边模式', edgeDock: true },
    ],
    predictions: ['把今天的任务排出优先级', '帮我拆成 3 个下一步动作', '总结我现在最该推进的事'],
  },
  {
    id: 'life',
    label: '生活',
    orbLabel: 'life agent',
    title: '生活代理在线',
    copy: '滚轮切换工作/生活/创作；现在更偏提醒、情绪和轻量陪伴。',
    voice: '生活模式已就绪，适合记录灵感、提醒和日常安排。',
    voiceStart: '生活模式收音中，优先记录提醒、情绪和日常安排。',
    voiceListening: '继续按住说话；我会把语音变成提醒、备忘或温柔提示。',
    voiceDone: '已停止收音，松开后会把内容整理成生活提醒。',
    chatTitle: '生活模式更像会陪你过日子的桌面精灵。',
    chatCopy: '它会优先记录灵感、安排提醒，语气也更柔和一点。',
    hint: '滚动切换工作/生活/创作形态；当前更偏提醒和陪伴。',
    menuTip: '生活模式下，顺时针画圈打开日常卡组；逆时针收回。',
    menuItems: [
      { title: '喝水', copy: '生成轻提醒，不打断节奏' },
      { title: '速记', copy: '收一条闪念或购物清单' },
      { title: '陪伴', copy: '切到更轻松的对话语气' },
      { title: '贴边', copy: '恢复不挡操作的贴边模式', edgeDock: true },
    ],
    predictions: ['提醒我今晚要做的 3 件事', '帮我记一下刚想到的清单', '给我一个轻松点的日程建议'],
  },
  {
    id: 'create',
    label: '创作',
    orbLabel: 'studio agent',
    title: '创作代理在线',
    copy: '滚轮切换工作/生活/创作；现在更偏灵感、草稿和发散联想。',
    voice: '创作模式已就绪，适合抓灵感、提概念和延展方向。',
    voiceStart: '创作模式收音中，优先保留比喻、关键词和未完成想法。',
    voiceListening: '继续按住说话；我会先保住灵感，再帮你扩成标题、提纲或概念。',
    voiceDone: '已停止收音，松开后会把灵感折成一段可继续发展的草稿。',
    chatTitle: '创作模式像一个帮你续写灵感的随身工作室。',
    chatCopy: '它会优先捕捉意象、节奏和关键词，把一瞬间的想法变成可延展的草稿。',
    hint: '滚动切换工作/生活/创作形态；当前更偏灵感和发散。',
    menuTip: '创作模式下，顺时针画圈打开灵感卡组；逆时针收回。',
    menuItems: [
      { title: '取题', copy: '把灵感扩成标题和命名' },
      { title: '提纲', copy: '一口气拉出结构骨架' },
      { title: '联想', copy: '发散比喻、风格和切入角度' },
      { title: '贴边', copy: '恢复不挡操作的贴边模式', edgeDock: true },
    ],
    predictions: ['把这个点子扩成 3 个方向', '先给我一个提纲草稿', '帮我发散几个有画面感的名字'],
  },
];

const state = {
  mode: 'idle',
  agentIndex: 0,
  dockSide: 'right',
  pressing: false,
  gestureArmed: false,
  moving: false,
  hiding: false,
  hidePrimed: false,
  hideTriggered: false,
  hidden: false,
  paused: false,
  smartDock: true,
  dockTimer: 0,
  hoverTimer: 0,
  predictionsVisible: false,
  thinking: false,
  replyVisible: false,
  streamTimer: 0,
  streamedReply: '',
  lastPrompt: '',
  lastReply: '',
  start: { x: 0, y: 0 },
  last: { x: 0, y: 0 },
  trace: [],
  hintShown: false,
  longPressTimer: 0,
  recognition: null,
  listening: false,
  speechSupported: false,
  interimTranscript: '',
  finalTranscript: '',
  stopRequested: false,
  wheelLocked: false,
};

const tauriWindow = safeWindow();
const app = document.querySelector('#app');

app.innerHTML = `
  <main class="shell idle">
    <section class="chat-panel panel">
      <div class="panel-head">
        <span class="eyebrow">Lift to speak</span>
        <button class="ghost" data-close>收起</button>
      </div>
      <div class="chat-card">
        <div class="signal-row">
          <span class="signal-dot"></span>
          <span class="voice-status" data-voice-state>长按悬浮球开始语音</span>
        </div>
        <h1 data-chat-title>它保持桌面悬浮，但只有你碰到球体时才打断操作。</h1>
        <p>
          <span data-chat-copy>轻按后直接滑动可以移动位置；长按会进入语音聆听，再通过上提或绕圈决定进入聊天还是功能卡组。</span>
        </p>
        <div class="transcript-chip" data-transcript-chip hidden></div>
        <div class="messages">
          <article>
            <span>你</span>
            <p data-user-example>帮我规划今天的任务。</p>
          </article>
          <article class="accent">
            <span>Orb</span>
            <p data-agent-example>我会先记录语音，再把结果自动带进对话框，减少切换成本。</p>
          </article>
        </div>
        <label class="composer">
          <textarea data-composer placeholder="长按说话，松开后内容会落在这里..."></textarea>
          <button type="button">发送</button>
        </label>
      </div>
    </section>

    <section class="menu-panel panel">
      <div class="panel-head">
        <span class="eyebrow">Orbit menu</span>
        <button class="ghost" data-close>隐藏</button>
      </div>
      <div class="menu-grid">
        <button type="button" data-menu-card="0">
          <strong data-menu-title="0">静音</strong>
          <span data-menu-copy="0">进入专注模式</span>
        </button>
        <button type="button" data-menu-card="1">
          <strong data-menu-title="1">速记</strong>
          <span data-menu-copy="1">一秒记下闪念</span>
        </button>
        <button type="button" data-menu-card="2">
          <strong data-menu-title="2">置顶</strong>
          <span data-menu-copy="2">保持前景悬浮</span>
        </button>
        <button type="button" data-menu-card="3" data-edge-dock>
          <strong data-menu-title="3">贴边</strong>
          <span data-menu-copy="3">恢复不挡操作的贴边模式</span>
        </button>
      </div>
      <p class="menu-tip" data-menu-tip>顺时针画圈打开卡组，逆时针画圈折叠；轻按拖动则只负责移动悬浮球。</p>
    </section>

    <section class="status-panel panel">
      <div class="panel-head">
        <span class="eyebrow">Orb status</span>
        <button class="ghost" data-close>收起</button>
      </div>
      <div class="status-card">
        <div class="signal-row">
          <span class="signal-dot"></span>
          <span data-status-line>桌面悬浮中，等待下一次唤起。</span>
        </div>
        <h1>这是悬浮球当前的状态页。</h1>
        <p>这里会收起一些你真正关心的东西：当前人格、最近一次语音、托盘状态和桌面待命方式。</p>
        <div class="status-metrics">
          <article>
            <span>人格</span>
            <strong data-status-agent>工作</strong>
          </article>
          <article>
            <span>托盘</span>
            <strong data-status-tray>桌面待命</strong>
          </article>
          <article>
            <span>语音</span>
            <strong data-status-voice>空闲</strong>
          </article>
          <article>
            <span>最近请求</span>
            <strong data-status-last>还没有发送内容</strong>
          </article>
        </div>
        <div class="status-log">
          <article>
            <span>悬浮逻辑</span>
            <p>贴边潜伏；鼠标靠近后会从边缘侧向探出。</p>
          </article>
          <article>
            <span>快捷手势</span>
            <p>上提进聊天，绕圈开卡组，向内拉出状态页，下滑缩进托盘。</p>
          </article>
        </div>
      </div>
    </section>

    <section class="orb-stage">
      <div class="orb-shadow"></div>
      <div class="halo-ring"></div>
      <div class="trail trail-a"></div>
      <div class="trail trail-b"></div>
      <button class="orb" aria-label="Orb Weave assistant">
        <span class="orb-core"></span>
        <span class="orb-mic"></span>
        <span class="agent-badge" data-agent-badge>工作</span>
        <span class="orb-label" data-orb-label>focus agent</span>
      </button>
      <div class="voice-dock">
        <span data-live-dot></span>
        <strong data-live-title>悬浮待命</strong>
        <p data-live-copy>轻按拖动位置，长按开始语音。</p>
      </div>
      <div class="hint-card">
        <strong>更像桌面精灵</strong>
        <p data-hint-copy>轻按滑动移动位置，长按说话，上提进聊天，绕圈开卡组。</p>
      </div>
      <div class="predict-card" data-predict-card>
        <strong>猜你想问</strong>
        <div class="predict-list">
          <button type="button" data-predict-item="0"></button>
          <button type="button" data-predict-item="1"></button>
          <button type="button" data-predict-item="2"></button>
        </div>
      </div>
      <div class="thinking-card" data-thinking-card>
        <div class="thinking-dots">
          <span></span>
          <span></span>
          <span></span>
        </div>
        <p>正在思考</p>
      </div>
      <button type="button" class="reply-card" data-reply-card>
        <span class="reply-kicker">快速回复</span>
        <p data-reply-stream></p>
        <small data-reply-hint>点击查看详情</small>
      </button>
    </section>
  </main>
`;

const shell = document.querySelector('.shell');
const orb = document.querySelector('.orb');
const orbStage = document.querySelector('.orb-stage');
const hintCard = document.querySelector('.hint-card');
const trails = [...document.querySelectorAll('.trail')];
const closeButtons = [...document.querySelectorAll('[data-close]')];
const composer = document.querySelector('[data-composer]');
const edgeDockButton = document.querySelector('[data-edge-dock]');
const transcriptChip = document.querySelector('[data-transcript-chip]');
const voiceState = document.querySelector('[data-voice-state]');
const liveTitle = document.querySelector('[data-live-title]');
const liveCopy = document.querySelector('[data-live-copy]');
const liveDot = document.querySelector('[data-live-dot]');
const agentBadge = document.querySelector('[data-agent-badge]');
const orbLabel = document.querySelector('[data-orb-label]');
const chatTitle = document.querySelector('[data-chat-title]');
const chatCopy = document.querySelector('[data-chat-copy]');
const hintCopy = document.querySelector('[data-hint-copy]');
const userExample = document.querySelector('[data-user-example]');
const agentExample = document.querySelector('[data-agent-example]');
const menuTip = document.querySelector('[data-menu-tip]');
const menuCards = [...document.querySelectorAll('[data-menu-card]')];
const menuTitles = [...document.querySelectorAll('[data-menu-title]')];
const menuCopies = [...document.querySelectorAll('[data-menu-copy]')];
const predictCard = document.querySelector('[data-predict-card]');
const predictItems = [...document.querySelectorAll('[data-predict-item]')];
const thinkingCard = document.querySelector('[data-thinking-card]');
const replyCard = document.querySelector('[data-reply-card]');
const replyStream = document.querySelector('[data-reply-stream]');
const replyHint = document.querySelector('[data-reply-hint]');
const statusLine = document.querySelector('[data-status-line]');
const statusAgent = document.querySelector('[data-status-agent]');
const statusTray = document.querySelector('[data-status-tray]');
const statusVoice = document.querySelector('[data-status-voice]');
const statusLast = document.querySelector('[data-status-last]');

predictCard.addEventListener('mouseenter', revealPredictions);
predictCard.addEventListener('mouseleave', hidePredictions);
replyCard.addEventListener('click', openReplyDetails);

boot();

function safeWindow() {
  try {
    return getCurrentWindow();
  } catch {
    return null;
  }
}

async function boot() {
  state.speechSupported = Boolean(window.SpeechRecognition || window.webkitSpeechRecognition);
  hydrateRecognition();
  await bindTrayEvents();
  shell.dataset.dock = state.dockSide;
  applyAgentMode();
  updateStatusPanel();
  await resizeWindow(ORB_SIZE, 'idle-peek');
  if (tauriWindow) {
    await tauriWindow.setAlwaysOnTop(true);
  }
  shell.classList.add('docked');
  await syncRuntimeStatus('idle');
  setTimeout(() => showHint(), 600);
}

orb.addEventListener('pointerdown', onPointerDown);
orb.addEventListener('wheel', onOrbWheel, { passive: false });
orbStage.addEventListener('pointerenter', onOrbEnter);
orbStage.addEventListener('pointerleave', onOrbLeave);
orbStage.addEventListener('mouseenter', onOrbHoverStart);
orbStage.addEventListener('mouseleave', onOrbHoverEnd);
window.addEventListener('pointermove', onPointerMove);
window.addEventListener('pointerup', onPointerUp);
window.addEventListener('pointercancel', onPointerUp);

closeButtons.forEach((button) => {
  button.addEventListener('click', () => switchMode('idle'));
});

menuCards.forEach((button, index) => {
  button.addEventListener('click', async () => {
    const item = AGENT_MODES[state.agentIndex].menuItems[index];
    if (item?.edgeDock) {
      state.smartDock = true;
      await switchMode('idle');
      return;
    }

    setVoiceHud(`${item.title} 已就绪`, item.copy);
  });
});

predictItems.forEach((button, index) => {
  button.addEventListener('click', async () => {
    const text = AGENT_MODES[state.agentIndex].predictions[index];
    injectTranscript(text);
    hidePredictions();
    await switchMode('chat');
  });
});

edgeDockButton.addEventListener('click', async () => {
  state.smartDock = true;
  await switchMode('idle');
});

async function onPointerDown(event) {
  if (state.paused || state.hidden) return;
  cancelHoverPrediction();
  hidePredictions(false);
  hideReplyCard();
  cancelDockTimer();
  state.pressing = true;
  state.gestureArmed = false;
  state.moving = false;
  state.hiding = false;
  state.hidePrimed = false;
  state.hideTriggered = false;
  state.start = { x: event.clientX, y: event.clientY };
  state.last = { x: event.clientX, y: event.clientY };
  state.trace = [{ x: event.clientX, y: event.clientY, t: Date.now() }];
  state.finalTranscript = '';
  state.interimTranscript = '';
  clearTimeout(state.longPressTimer);
  state.longPressTimer = window.setTimeout(armGestureMode, LONG_PRESS_MS);
  orb.setPointerCapture(event.pointerId);
  hideHint();
  shell.className = 'shell ready docked';
  setVoiceHud('准备中', '继续按住会进入语音；现在滑动则移动悬浮球。');
  await resizeWindow(PREVIEW_SIZE, 'preview');
}

function onOrbWheel(event) {
  event.preventDefault();
  if (state.wheelLocked || state.pressing || state.gestureArmed || state.paused || state.hidden) return;

  const direction = event.deltaY > 0 ? 1 : -1;
  state.agentIndex = (state.agentIndex + direction + AGENT_MODES.length) % AGENT_MODES.length;
  applyAgentMode(true);
}

async function onPointerMove(event) {
  if (!state.pressing) return;

  state.last = { x: event.clientX, y: event.clientY };
  state.trace.push({ x: event.clientX, y: event.clientY, t: Date.now() });
  if (state.trace.length > 28) {
    state.trace.shift();
  }

  const dx = event.clientX - state.start.x;
  const dy = event.clientY - state.start.y;
  const lift = Math.max(0, -dy);
  const distance = Math.hypot(dx, dy);
  const progress = Math.min(lift / 180, 1);
  const pullAway = state.dockSide === 'left' ? dx > 48 : dx < -48;
  const verticalHideTrack = dy > HIDE_PRIME_Y && Math.abs(dy) > Math.abs(dx) * 1.15;

  if (!state.gestureArmed && verticalHideTrack) {
    state.hidePrimed = true;
    shell.className = 'shell ready';
    setVoiceHud('下滑可隐藏', '继续向下滑动，松手后会缩进托盘。');
  }

  if (!state.gestureArmed && dy > HIDE_SWIPE_Y && Math.abs(dx) < HIDE_SWIPE_X) {
    await beginHideToTray();
    return;
  }

  if (state.hiding) {
    return;
  }

  if (!state.gestureArmed && state.hidePrimed) {
    return;
  }

  if (!state.gestureArmed && distance > DRAG_THRESHOLD) {
    clearTimeout(state.longPressTimer);
    state.moving = true;
    state.pressing = false;
    shell.className = 'shell moving';
    setVoiceHud('正在移动', '松手后会自动吸附到最近边缘。');
    await startWindowDrag();
    await finishDragDock();
    return;
  }

  if (!state.gestureArmed) return;

  shell.style.setProperty('--drag-x', `${dx}px`);
  shell.style.setProperty('--drag-y', `${dy}px`);
  shell.style.setProperty('--lift', progress.toFixed(3));
  shell.className = 'shell listening';
  shell.classList.toggle('tracking-chat', lift > 30 && Math.abs(dx) < 120);
  shell.classList.toggle('tracking-menu', distance > 30);
  shell.classList.toggle('tracking-status', pullAway && Math.abs(dy) < 120);

  trails[0].style.transform = `translate(${dx * 0.18}px, ${dy * 0.18}px) scale(${1 + progress * 0.12})`;
  trails[1].style.transform = `translate(${dx * -0.12}px, ${dy * -0.12}px) scale(${1 + progress * 0.2})`;
}

async function onPointerUp() {
  clearTimeout(state.longPressTimer);
  if (!state.pressing && !state.gestureArmed) {
    settleIdleState();
    return;
  }

  const dx = state.last.x - state.start.x;
  const dy = state.last.y - state.start.y;
  const lift = -dy;
  const orbit = detectOrbit(state.trace);
  const heardText = getTranscript();
  const pullAway = state.dockSide === 'left' ? dx > 96 : dx < -96;

  state.pressing = false;
  shell.style.removeProperty('--drag-x');
  shell.style.removeProperty('--drag-y');
  shell.style.removeProperty('--lift');
  shell.classList.remove('tracking-chat', 'tracking-menu');

  if (state.gestureArmed) {
    stopVoiceInput();
  }
  state.gestureArmed = false;
  state.hidePrimed = false;
  state.hideTriggered = false;
  shell.classList.remove('tracking-status');

  if (state.hiding) {
    return;
  }

  if (orbit > 0.7) {
    await switchMode('menu');
  } else if (orbit < -0.7) {
    await switchMode('idle');
  } else if (pullAway && Math.abs(dy) < 110) {
    await switchMode('status');
  } else if (heardText) {
    await submitVoicePrompt(heardText);
  } else if (lift > 96) {
    await switchMode('chat');
  } else {
    await pulse();
    settleIdleState();
  }
}

function armGestureMode() {
  if (!state.pressing || state.moving) return;
  state.gestureArmed = true;
  shell.className = `shell listening${state.smartDock ? ' docked revealed' : ''}`;
  startVoiceInput();
}

async function onOrbEnter() {
  if (state.mode !== 'idle' || !state.smartDock || state.pressing) return;
  cancelDockTimer();
  shell.classList.add('revealed');
  shell.classList.remove('docked');
  await resizeWindow(PREVIEW_SIZE, 'preview');
}

function onOrbLeave() {
  if (state.mode !== 'idle' || !state.smartDock || state.pressing) return;
  if (state.predictionsVisible) return;
  scheduleDockPeek();
}

function onOrbHoverStart() {
  if (state.hidden || state.paused || state.pressing || state.mode !== 'idle') return;
  cancelHoverPrediction();
  state.hoverTimer = window.setTimeout(() => {
    if (state.hidden || state.paused || state.pressing || state.mode !== 'idle') return;
    revealPredictions();
  }, HOVER_PREDICT_MS);
}

function onOrbHoverEnd() {
  cancelHoverPrediction();
  if (!predictCard.matches(':hover')) {
    hidePredictions(false);
  }
}

function hydrateRecognition() {
  const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (!SpeechRecognition) return;

  const recognition = new SpeechRecognition();
  recognition.lang = 'zh-CN';
  recognition.continuous = true;
  recognition.interimResults = true;
  recognition.maxAlternatives = 1;
  recognition.onstart = () => {
    state.listening = true;
    shell.classList.add('voice-live');
    const agent = AGENT_MODES[state.agentIndex];
    setVoiceHud('正在聆听', agent.voiceListening);
  };
  recognition.onresult = (event) => {
    let interim = '';
    let finalText = state.finalTranscript;
    for (let index = event.resultIndex; index < event.results.length; index += 1) {
      const transcript = event.results[index][0].transcript.trim();
      if (event.results[index].isFinal) {
        finalText = `${finalText} ${transcript}`.trim();
      } else {
        interim = `${interim} ${transcript}`.trim();
      }
    }
    state.finalTranscript = finalText;
    state.interimTranscript = interim;
    const liveText = getTranscript();
    if (liveText) {
      setVoiceHud('语音转写中', liveText);
    }
  };
  recognition.onerror = (event) => {
    const errors = {
      'not-allowed': '麦克风权限被拒绝，请允许录音后重试。',
      'no-speech': '没有检测到语音，可以再长按一次。',
      'audio-capture': '没有找到可用麦克风。',
    };
    setVoiceHud('语音不可用', errors[event.error] || '语音识别被中断，请重试。');
  };
  recognition.onend = () => {
    state.listening = false;
    shell.classList.remove('voice-live');
    if (!state.stopRequested && state.gestureArmed) {
      const agent = AGENT_MODES[state.agentIndex];
      setVoiceHud('聆听结束', agent.voiceDone);
    }
    state.stopRequested = false;
  };

  state.recognition = recognition;
}

function startVoiceInput() {
  if (!state.speechSupported || !state.recognition) {
    setVoiceHud('语音不可用', '当前 WebView 不支持语音识别，但长按手势仍可用。');
    voiceState.textContent = '当前环境不支持 Web Speech API';
    liveDot.classList.remove('active');
    return;
  }

  try {
    const agent = AGENT_MODES[state.agentIndex];
    state.finalTranscript = '';
    state.interimTranscript = '';
    state.stopRequested = false;
    state.recognition.start();
    voiceState.textContent = agent.voiceStart;
    setVoiceHud('正在聆听', agent.voiceListening);
    liveDot.classList.add('active');
  } catch {
    setVoiceHud('语音启动失败', '可能是浏览器限制了重复启动，请稍后再试。');
  }
}

function stopVoiceInput() {
  if (!state.recognition || !state.listening) {
    liveDot.classList.remove('active');
    return;
  }

  state.stopRequested = true;
  state.recognition.stop();
  liveDot.classList.remove('active');
}

function getTranscript() {
  return `${state.finalTranscript} ${state.interimTranscript}`.trim();
}

function injectTranscript(text) {
  if (!text) {
    transcriptChip.hidden = true;
    return;
  }

  composer.value = text;
  transcriptChip.hidden = false;
  transcriptChip.textContent = `语音已写入: ${text}`;
  voiceState.textContent = '已把语音送入输入框';
}

async function submitVoicePrompt(text) {
  state.lastPrompt = text;
  injectTranscript(text);
  hidePredictions(false);
  showThinking();
  await resizeWindow(PREVIEW_SIZE, 'preview');
  await syncRuntimeStatus('running');
  const reply = buildAgentReply(text);
  await streamAgentReply(reply);
}

function buildAgentReply(text) {
  const agent = AGENT_MODES[state.agentIndex];
  if (agent.id === 'work') {
    return `我先帮你抓到重点：${text}。建议先明确目标和截止时间，再立刻执行一个最小动作。我已经整理好摘要，点开详情可以继续拆分成待办。`;
  }

  if (agent.id === 'life') {
    return `我收到的是：${text}。先帮你收成一个轻量提醒，再补一条更省心的安排建议。点开详情，我可以继续帮你扩成提醒或清单。`;
  }

  return `这个灵感我先替你保住：${text}。它适合往意象、标题和结构三个方向继续扩展。我已经起了一个短草稿，点开详情就能继续展开。`;
}

async function streamAgentReply(reply) {
  clearInterval(state.streamTimer);
  state.streamedReply = '';
  state.lastReply = reply;
  hideThinking();
  showReplyCard();
  replyStream.textContent = '';
  replyHint.textContent = '回复生成中...';

  await new Promise((resolve) => {
    let index = 0;
    state.streamTimer = window.setInterval(() => {
      index += 2;
      state.streamedReply = reply.slice(0, index);
      replyStream.textContent = state.streamedReply;
      if (index >= reply.length) {
        clearInterval(state.streamTimer);
        state.streamTimer = 0;
        replyHint.textContent = '点击查看详情';
        appendReplyToChat();
        setVoiceHud('回复已到达', '右上角的小卡片可展开查看完整对话。');
        syncRuntimeStatus('idle');
        resolve();
      }
    }, STREAM_TICK_MS);
  });
}

function appendReplyToChat() {
  transcriptChip.hidden = false;
  transcriptChip.textContent = `已发送给 AI: ${state.lastPrompt}`;
  userExample.textContent = state.lastPrompt;
  agentExample.textContent = state.lastReply;
  updateStatusPanel();
}

function showThinking() {
  hideReplyCard();
  state.thinking = true;
  shell.classList.add('thinking');
  thinkingCard.classList.add('visible');
  setVoiceHud('正在思考', '语音已直接发送给 AI。');
  updateStatusPanel();
}

function hideThinking() {
  state.thinking = false;
  shell.classList.remove('thinking');
  thinkingCard.classList.remove('visible');
}

function showReplyCard() {
  state.replyVisible = true;
  shell.classList.add('replying');
  replyCard.classList.add('visible');
}

function hideReplyCard() {
  state.replyVisible = false;
  shell.classList.remove('replying');
  replyCard.classList.remove('visible');
}

function updateStatusPanel() {
  const agent = AGENT_MODES[state.agentIndex];
  statusLine.textContent = state.replyVisible
    ? '右上角有一条最新回复，可点击查看详情。'
    : state.thinking
      ? 'AI 正在处理最近一次语音请求。'
      : '桌面悬浮中，等待下一次唤起。';
  statusAgent.textContent = agent.label;
  statusTray.textContent = state.hidden ? '已缩进托盘' : '桌面待命';
  statusVoice.textContent = state.listening ? '正在收音' : state.thinking ? '已发送，等待回复' : '空闲';
  statusLast.textContent = state.lastPrompt || '还没有发送内容';
}

async function openReplyDetails() {
  if (!state.lastReply) return;
  composer.value = state.lastPrompt;
  appendReplyToChat();
  hideReplyCard();
  await switchMode('chat');
}

function detectOrbit(points) {
  if (points.length < 10) return 0;

  let angle = 0;
  for (let index = 1; index < points.length - 1; index += 1) {
    const a = points[index - 1];
    const b = points[index];
    const c = points[index + 1];
    const ab = { x: b.x - a.x, y: b.y - a.y };
    const bc = { x: c.x - b.x, y: c.y - b.y };
    angle += Math.atan2(ab.x * bc.y - ab.y * bc.x, ab.x * bc.x + ab.y * bc.y);
  }

  return angle / (Math.PI * 2);
}

async function switchMode(mode) {
  state.mode = mode;
  shell.className = `shell ${mode}${state.smartDock ? ' docked' : ''}`;
  await syncRuntimeStatus(mode === 'idle' ? 'idle' : 'running');
  if (mode === 'chat') {
    setVoiceHud('聊天已展开', '继续说话会先写进输入框，再进入完整对话。');
    await resizeWindow(CHAT_SIZE, 'chat');
    composer.focus();
  } else if (mode === 'menu') {
    setVoiceHud('功能卡组已展开', '这时不会挡住桌面太多区域，用完可逆时针绕圈收回。');
    await resizeWindow(MENU_SIZE, 'menu');
  } else if (mode === 'status') {
    updateStatusPanel();
    setVoiceHud('状态页已展开', '这里集中显示悬浮球当前状态和最近动作。');
    await resizeWindow(STATUS_SIZE, 'status');
  } else {
    settleIdleState();
    await resizeWindow(ORB_SIZE, state.smartDock ? 'idle-peek' : 'free');
  }
}

function settleIdleState() {
  cancelDockTimer();
  cancelHoverPrediction();
  const agent = AGENT_MODES[state.agentIndex];
  state.mode = 'idle';
  state.pressing = false;
  state.moving = false;
  state.hiding = false;
  state.hidePrimed = false;
  state.hideTriggered = false;
  state.gestureArmed = false;
  shell.className = `shell idle${state.smartDock ? ' docked' : ''}`;
  setVoiceHud(agent.title, agent.copy);
  syncRuntimeStatus('idle');
  hidePredictions(false);
  hideThinking();
  hideReplyCard();
  if (state.smartDock) {
    scheduleDockPeek(80);
  }
}

async function resizeWindow(size, mode = 'free') {
  if (!tauriWindow) return;
  const logicalSize = new LogicalSize(size.width, size.height);
  await tauriWindow.setSize(logicalSize);
  await tauriWindow.setMinSize(logicalSize);
  await syncDock(mode, size);
}

async function syncDock(mode, size = ORB_SIZE) {
  if (!tauriWindow) return;
  try {
    const dockSide = await invoke('sync_window_layout', { width: size.width, height: size.height, mode });
    if (dockSide === 'left' || dockSide === 'right') {
      state.dockSide = dockSide;
      shell.dataset.dock = dockSide;
    }
  } catch {
    // Ignore when running in plain browser mode.
  }
}

async function startWindowDrag() {
  if (!tauriWindow) return;
  try {
    await tauriWindow.startDragging();
  } catch {
    // Ignore drag failures in browser mode.
  }
}

async function finishDragDock() {
  state.smartDock = true;
  state.moving = false;
  state.pressing = false;
  state.gestureArmed = false;
  state.hiding = false;
  state.hidePrimed = false;
  state.hideTriggered = false;
  hidePredictions(false);
  hideThinking();
  shell.style.removeProperty('--drag-x');
  shell.style.removeProperty('--drag-y');
  shell.style.removeProperty('--lift');
  shell.className = 'shell idle docked';
  setVoiceHud('已贴边待命', '拖动结束后会自动吸附到最近边缘。');
  await resizeWindow(ORB_SIZE, 'idle-peek');
  scheduleDockPeek(80);
}

async function hideIntoTray() {
  state.hidden = true;
  state.pressing = false;
  state.gestureArmed = false;
  state.moving = false;
  state.hiding = false;
  state.hidePrimed = false;
  state.hideTriggered = false;
  hidePredictions();
  hideThinking();
  hideReplyCard();
  shell.className = 'shell hidden';
  await syncRuntimeStatus(state.paused ? 'paused' : 'idle');
  try {
    await invoke('hide_to_tray');
  } catch {
    state.hidden = false;
    settleIdleState();
  }
}

async function beginHideToTray() {
  if (state.hiding || state.hideTriggered || state.hidden) return;
  clearTimeout(state.longPressTimer);
  stopVoiceInput();
  state.hideTriggered = true;
  state.hidePrimed = false;
  state.hiding = true;
  state.pressing = false;
  state.gestureArmed = false;
  state.moving = false;
  shell.className = 'shell hiding';
  setVoiceHud('正在隐藏', '正在吸入托盘，点击托盘图标可恢复。');
  await new Promise((resolve) => setTimeout(resolve, HIDE_ANIMATION_MS));
  await hideIntoTray();
}

async function bindTrayEvents() {
  try {
    await listen('tray://show', async () => {
      state.hidden = false;
      settleIdleState();
      await resizeWindow(ORB_SIZE, 'idle-peek');
    });
    await listen('tray://hide', () => {
      state.hidden = true;
      shell.className = 'shell hidden';
    });
    await listen('tray://pause', (event) => {
      state.paused = Boolean(event.payload?.paused);
      if (state.paused) {
        stopVoiceInput();
        state.pressing = false;
        state.gestureArmed = false;
        shell.className = 'shell paused';
        setVoiceHud('已暂停', '托盘菜单中选择“继续”后恢复。');
        syncRuntimeStatus('paused');
      } else {
        settleIdleState();
      }
    });
  } catch {
    // Ignore in plain browser mode.
  }
}

async function syncRuntimeStatus(status) {
  if (!tauriWindow) return;
  try {
    await invoke('set_runtime_status', { status });
  } catch {
    // Ignore in plain browser mode.
  }
}

async function pulse() {
  shell.classList.add('pulse');
  await new Promise((resolve) => setTimeout(resolve, 240));
  shell.classList.remove('pulse');
}

function applyAgentMode(withPulse = false) {
  const agent = AGENT_MODES[state.agentIndex];
  shell.dataset.agent = agent.id;
  agentBadge.textContent = agent.label;
  orbLabel.textContent = agent.orbLabel;
  chatTitle.textContent = agent.chatTitle;
  chatCopy.textContent = agent.chatCopy;
  hintCopy.textContent = agent.hint;
  voiceState.textContent = agent.voice;
  userExample.textContent = agent.id === 'work'
    ? '帮我把今天的任务排出优先级。'
    : agent.id === 'life'
      ? '提醒我今晚买水果，顺便记录一个周末计划。'
      : '把“会呼吸的桌面精灵”扩成三个视觉概念。';
  agentExample.textContent = agent.id === 'work'
    ? '我会先提炼截止时间、阻塞点和下一步动作，再收成执行清单。'
    : agent.id === 'life'
      ? '我会先保留提醒和情绪线索，让安排更轻一点，也更像陪伴。'
      : '我会先保住意象和关键词，再帮你延展成题目、提纲或风格方向。';
  menuTip.textContent = agent.menuTip;
  agent.menuItems.forEach((item, index) => {
    menuTitles[index].textContent = item.title;
    menuCopies[index].textContent = item.copy;
  });
  agent.predictions.forEach((item, index) => {
    predictItems[index].textContent = item;
  });

  if (state.mode === 'idle') {
    setVoiceHud(agent.title, agent.copy);
  }

  if (withPulse) {
    flashAgentSwitch(agent);
  }
}

function flashAgentSwitch(agent) {
  state.wheelLocked = true;
  shell.classList.remove('agent-shift');
  void shell.offsetWidth;
  shell.classList.add('agent-shift');
  setVoiceHud(`${agent.label}模式`, agent.copy);
  window.setTimeout(() => {
    shell.classList.remove('agent-shift');
    state.wheelLocked = false;
    if (state.mode === 'idle') {
      setVoiceHud(agent.title, agent.copy);
    }
  }, 380);
}

function setVoiceHud(title, copy) {
  liveTitle.textContent = title;
  liveCopy.textContent = copy;
}

function showHint() {
  if (state.hintShown) return;
  state.hintShown = true;
  hintCard.classList.add('visible');
}

function hideHint() {
  hintCard.classList.remove('visible');
}

async function revealPredictions() {
  if (state.predictionsVisible) return;
  state.predictionsVisible = true;
  predictCard.classList.add('visible');
  shell.classList.add('predicting');
  await resizeWindow(PREVIEW_SIZE, 'preview');
}

function hidePredictions(scheduleDock = true) {
  state.predictionsVisible = false;
  predictCard.classList.remove('visible');
  shell.classList.remove('predicting');
  if (
    scheduleDock &&
    state.mode === 'idle' &&
    state.smartDock &&
    !state.pressing &&
    !state.hidden &&
    !state.paused &&
    !state.replyVisible &&
    !state.thinking
  ) {
    scheduleDockPeek(80);
  }
}

function cancelHoverPrediction() {
  clearTimeout(state.hoverTimer);
}

function scheduleDockPeek(delay = 220) {
  cancelDockTimer();
  state.dockTimer = window.setTimeout(async () => {
    if (
      state.mode !== 'idle' ||
      !state.smartDock ||
      state.pressing ||
      state.predictionsVisible ||
      state.replyVisible ||
      state.thinking
    ) return;
    hidePredictions();
    shell.classList.add('docked');
    shell.classList.remove('revealed');
    await resizeWindow(ORB_SIZE, 'idle-peek');
  }, delay);
}

function cancelDockTimer() {
  clearTimeout(state.dockTimer);
}
