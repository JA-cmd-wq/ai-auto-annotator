import type { GlobalThemeOverrides } from 'naive-ui'

export const apple = {
  blue: '#0066cc',
  blueFocus: '#0071e3',
  ink: '#1d1d1f',
  muted: '#6e6e73',
  hairline: '#e0e0e0',
  canvas: '#ffffff',
  parchment: '#f5f5f7',
  pearl: '#fafafc',
  black: '#000000'
}

export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: apple.blue,
    primaryColorHover: apple.blueFocus,
    primaryColorPressed: apple.blue,
    primaryColorSuppl: apple.blueFocus,
    borderRadius: '8px',
    fontFamily: 'SF Pro Text, -apple-system, BlinkMacSystemFont, Inter, Microsoft YaHei, sans-serif',
    textColorBase: apple.ink
  },
  Button: {
    borderRadiusMedium: '9999px',
    paddingMedium: '0 18px',
    heightMedium: '40px',
    fontSizeMedium: '14px',
    fontWeight: '400'
  },
  Card: {
    borderRadius: '8px',
    borderColor: apple.hairline,
    color: apple.canvas,
    boxShadow: 'none'
  },
  Input: {
    borderRadius: '9999px',
    borderHover: `1px solid ${apple.blueFocus}`,
    borderFocus: `1px solid ${apple.blueFocus}`,
    boxShadowFocus: '0 0 0 4px rgba(0, 113, 227, 0.12)'
  },
  Tag: {
    borderRadius: '9999px'
  },
  Menu: {
    itemTextColor: apple.ink,
    itemTextColorActive: apple.blue,
    itemIconColorActive: apple.blue,
    itemColorActive: '#eaf2fd'
  }
}
