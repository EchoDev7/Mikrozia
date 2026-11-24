import { createContext, useCallback, useContext, useLayoutEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { theme as antdTheme } from 'antd';
import type { ThemeConfig } from 'antd';

const STORAGE_DARK = 'dark-mode';
const STORAGE_ULTRA = 'isUltraDarkThemeEnabled';

function readBool(key: string, fallback: boolean): boolean {
  const raw = localStorage.getItem(key);
  if (raw === null) return fallback;
  return raw === 'true';
}

function applyDom(isDark: boolean, isUltra: boolean) {
  document.body.classList.remove('dark', 'light');
  document.body.classList.add(isDark ? 'dark' : 'light');
  // Native scrollbars read color-scheme, not the body class.
  document.documentElement.style.colorScheme = isDark ? 'dark' : 'light';
  if (isUltra) {
    document.documentElement.setAttribute('data-theme', 'ultra-dark');
  } else {
    document.documentElement.removeAttribute('data-theme');
  }
  const msg = document.getElementById('message');
  if (msg) {
    msg.classList.remove('dark', 'light');
    msg.classList.add(isDark ? 'dark' : 'light');
  }
}

// module load so the document is in the right theme before React mounts.
const initialDark = readBool(STORAGE_DARK, true);
const initialUltra = readBool(STORAGE_ULTRA, false);
applyDom(initialDark, initialUltra);

const SHARED_SHAPE_TOKENS = {
  borderRadius: 0,
  borderRadiusLG: 0,
  borderRadiusSM: 0,
  borderRadiusXS: 0,
  fontFamily:
    "'Space Grotesk', 'Vazirmatn', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  fontFamilyCode:
    "'IBM Plex Mono', ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
};

const LIGHT_TOKENS = {
  ...SHARED_SHAPE_TOKENS,
  colorPrimary: '#B81D3A',
  colorInfo: '#213B5E',
  colorSuccess: '#2F9E44',
  colorWarning: '#D9A426',
  colorError: '#B81D3A',
  colorErrorText: '#B81D3A',
  colorSuccessText: '#237804',
  colorBgBase: '#F5F0E6',
  colorBgLayout: '#F5F0E6',
  colorBgContainer: '#FAF7F0',
  colorBgElevated: '#FAF7F0',
  colorText: '#111111',
  colorTextSecondary: '#4A463F',
  colorTextDescription: '#524E47',
  colorTextTertiary: '#5F5A52',
  colorTextPlaceholder: '#6B655C',
  colorBorder: '#111111',
  colorBorderSecondary: '#2C2A27',
};
const LIGHT_LAYOUT_TOKENS = {
  bodyBg: '#F5F0E6',
  headerBg: '#F5F0E6',
  headerColor: '#111111',
  footerBg: '#F5F0E6',
  siderBg: '#F5F0E6',
  triggerBg: '#FAF7F0',
  triggerColor: '#111111',
};
const LIGHT_MENU_TOKENS = {
  itemBg: '#F5F0E6',
  subMenuItemBg: '#EFEAE0',
  popupBg: '#FAF7F0',
  itemBorderRadius: 0,
  subMenuItemBorderRadius: 0,
};
const LIGHT_CARD_TOKENS = {
  colorBorderSecondary: '#111111',
};
const LIGHT_TABLE_TOKENS = {
  headerBg: '#111111',
  headerColor: '#FAF7F0',
  headerSortActiveBg: '#213B5E',
  headerSortHoverBg: '#213B5E',
  borderColor: '#111111',
  rowHoverBg: '#EFEAE0',
};
const LIGHT_BUTTON_TOKENS = {
  colorPrimary: '#B81D3A',
  colorPrimaryHover: '#9E1730',
  colorPrimaryActive: '#821227',
  defaultBg: '#FAF7F0',
  defaultBorderColor: '#111111',
  defaultColor: '#111111',
};

const DARK_TOKENS = {
  ...SHARED_SHAPE_TOKENS,
  colorPrimary: '#D92B4B',
  colorInfo: '#3E73AD',
  colorSuccess: '#38B857',
  colorWarning: '#E5B32E',
  colorError: '#D92B4B',
  colorBgBase: '#111215',
  colorBgLayout: '#111215',
  colorBgContainer: '#17191E',
  colorBgElevated: '#1F2229',
  colorText: '#F5F0E6',
  colorTextSecondary: '#B8BECB',
  colorTextTertiary: '#8E95A5',
  colorBorder: '#3B404D',
  colorBorderSecondary: '#2C303B',
};
const ULTRA_DARK_TOKENS = {
  ...SHARED_SHAPE_TOKENS,
  colorPrimary: '#D92B4B',
  colorInfo: '#3E73AD',
  colorSuccess: '#38B857',
  colorWarning: '#E5B32E',
  colorError: '#D92B4B',
  colorBgBase: '#000000',
  colorBgLayout: '#000000',
  colorBgContainer: '#0D0E12',
  colorBgElevated: '#15171D',
  colorText: '#F5F0E6',
  colorTextSecondary: '#B8BECB',
  colorTextTertiary: '#8E95A5',
  colorBorder: '#2E323D',
  colorBorderSecondary: '#1F222A',
};
const DARK_LAYOUT_TOKENS = {
  bodyBg: '#111215',
  headerBg: '#14161B',
  headerColor: '#F5F0E6',
  footerBg: '#111215',
  siderBg: '#14161B',
  triggerBg: '#17191E',
  triggerColor: '#F5F0E6',
};
const ULTRA_DARK_LAYOUT_TOKENS = {
  bodyBg: '#000000',
  headerBg: '#050507',
  headerColor: '#F5F0E6',
  footerBg: '#000000',
  siderBg: '#050507',
  triggerBg: '#15171D',
  triggerColor: '#F5F0E6',
};
const DARK_MENU_TOKENS = {
  darkItemBg: '#14161B',
  darkSubMenuItemBg: '#111215',
  darkPopupBg: '#1F2229',
  itemBorderRadius: 0,
  subMenuItemBorderRadius: 0,
};
const ULTRA_DARK_MENU_TOKENS = {
  darkItemBg: '#050507',
  darkSubMenuItemBg: '#000000',
  darkPopupBg: '#0D0E12',
  itemBorderRadius: 0,
  subMenuItemBorderRadius: 0,
};
const DARK_CARD_TOKENS = {
  colorBorderSecondary: '#3B404D',
};
const ULTRA_DARK_CARD_TOKENS = {
  colorBorderSecondary: '#2E323D',
};
const DARK_TABLE_TOKENS = {
  headerBg: '#213B5E',
  headerColor: '#F5F0E6',
  headerSortActiveBg: '#2B4D7A',
  headerSortHoverBg: '#2B4D7A',
  borderColor: '#2C303B',
  rowHoverBg: '#1D2028',
};
const ULTRA_DARK_TABLE_TOKENS = {
  headerBg: '#1B314E',
  headerColor: '#F5F0E6',
  headerSortActiveBg: '#244166',
  headerSortHoverBg: '#244166',
  borderColor: '#1F222A',
  rowHoverBg: '#14161C',
};
const STATISTIC_TOKENS = {
  contentFontSize: 17,
  titleFontSize: 11,
};

// hashed:false drops the `:where(.css-<hash>)` wrapper antd puts around every
// rule. It costs nothing in specificity — `:where()` contributes zero, so the
// panel's own `.ant-*` overrides still win — and it removes roughly 5,700
// wrappers, 16% of the generated stylesheet, from what the browser has to parse.
//
// cssVar.key pins the CSS-variable scope. Every panel page mounts its own
// ConfigProvider (there is no root one), and without a fixed key each mints a
// fresh useId-derived scope, so navigating re-serialises and re-injects the whole
// token block under a new class instead of reusing the one already in the head.
const SHARED_STYLE_CONFIG = {
  hashed: false,
  cssVar: { key: 'xui' },
} as const;

export function buildAntdThemeConfig(isDark: boolean, isUltra: boolean): ThemeConfig {
  if (!isDark) {
    return {
      ...SHARED_STYLE_CONFIG,
      algorithm: antdTheme.defaultAlgorithm,
      token: LIGHT_TOKENS,
      components: {
        Layout: LIGHT_LAYOUT_TOKENS,
        Menu: LIGHT_MENU_TOKENS,
        Card: LIGHT_CARD_TOKENS,
        Table: LIGHT_TABLE_TOKENS,
        Statistic: STATISTIC_TOKENS,
        Button: LIGHT_BUTTON_TOKENS,
      },
    };
  }
  return {
    ...SHARED_STYLE_CONFIG,
    algorithm: antdTheme.darkAlgorithm,
    token: isUltra ? ULTRA_DARK_TOKENS : DARK_TOKENS,
    components: {
      Layout: isUltra ? ULTRA_DARK_LAYOUT_TOKENS : DARK_LAYOUT_TOKENS,
      Menu: isUltra ? ULTRA_DARK_MENU_TOKENS : DARK_MENU_TOKENS,
      Card: isUltra ? ULTRA_DARK_CARD_TOKENS : DARK_CARD_TOKENS,
      Table: isUltra ? ULTRA_DARK_TABLE_TOKENS : DARK_TABLE_TOKENS,
      Statistic: STATISTIC_TOKENS,
    },
  };
}

export function pauseAnimationsUntilLeave(elementId: string): void {
  document.documentElement.setAttribute('data-theme-animations', 'off');
  const el = document.getElementById(elementId);
  if (!el) return;
  const restore = () => {
    document.documentElement.removeAttribute('data-theme-animations');
    el.removeEventListener('mouseleave', restore);
    el.removeEventListener('touchend', restore);
  };
  el.addEventListener('mouseleave', restore);
  el.addEventListener('touchend', restore);
}

interface ThemeContextValue {
  isDark: boolean;
  isUltra: boolean;
  toggleTheme: () => void;
  toggleUltra: () => void;
  antdThemeConfig: ThemeConfig;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [isDark, setIsDark] = useState<boolean>(initialDark);
  const [isUltra, setIsUltra] = useState<boolean>(initialUltra);

  useLayoutEffect(() => {
    applyDom(isDark, isUltra);
    localStorage.setItem(STORAGE_DARK, String(isDark));
    localStorage.setItem(STORAGE_ULTRA, String(isUltra));
  }, [isDark, isUltra]);

  const toggleTheme = useCallback(() => setIsDark((v) => !v), []);
  const toggleUltra = useCallback(() => setIsUltra((v) => !v), []);

  const antdThemeConfig = useMemo(() => buildAntdThemeConfig(isDark, isUltra), [isDark, isUltra]);

  const value = useMemo<ThemeContextValue>(
    () => ({ isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig }),
    [isDark, isUltra, toggleTheme, toggleUltra, antdThemeConfig],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used inside <ThemeProvider>');
  return ctx;
}
