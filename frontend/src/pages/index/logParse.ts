// Parser for the panel in-memory application log viewer.
// App-log lines use "YYYY/MM/DD HH:MM:SS LEVEL - message"; plain lines render verbatim.

export interface ParsedLog {
  date: string;
  time: string;
  stamp: string;
  levelText: string;
  levelClass: string;
  service: string;
  body: string;
}

export const LEVELS = ['DEBUG', 'INFO', 'NOTICE', 'WARNING', 'ERROR'];
export const LEVEL_CLASSES = [
  'level-debug',
  'level-info',
  'level-notice',
  'level-warning',
  'level-error',
];

const APP_LOG = /^(\d{4}\/\d{2}\/\d{2})\s+(\d{2}:\d{2}:\d{2})\s+(\S+)\s+-\s+([\s\S]*)$/;

export function parseLogLine(line: string): ParsedLog {
  const raw = (line || '').trim();

  let date = '';
  let time = '';
  let levelText = '';
  let body: string;

  const app = raw.match(APP_LOG);
  if (app) {
    date = app[1];
    time = app[2];
    levelText = app[3];
    body = app[4];
  } else {
    body = raw;
  }

  const li = LEVELS.indexOf(levelText);
  const levelClass = li >= 0 ? LEVEL_CLASSES[li] : 'level-unknown';

  let service = '';
  if (body.startsWith('XRAY:')) {
    service = 'XRAY:';
    body = body.slice('XRAY:'.length).trimStart();
  } else if (body) {
    service = 'X-UI:';
  }

  const stamp = [date, time].filter(Boolean).join(' ');

  return { date, time, stamp, levelText, levelClass, service, body };
}
