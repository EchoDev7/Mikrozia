import type { CSSProperties } from 'react';

interface MikroziaLogoProps {
  isDark?: boolean;
  height?: number;
  className?: string;
  style?: CSSProperties;
}

export function MikroziaLogo({ isDark = false, height = 24, className, style }: MikroziaLogoProps) {
  const ink = isDark ? '#F5F0E6' : '#111111';
  const crimson = isDark ? '#D92B4B' : '#B81D3A';
  const navy = isDark ? '#3E73AD' : '#213B5E';
  const mustard = isDark ? '#E5B32E' : '#D9A426';

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 402 96"
      height={height}
      className={className}
      style={{ display: 'block', width: 'auto', flexShrink: 0, ...style }}
      role="img"
      aria-label="Mikrozia"
    >
      <path
        fill={ink}
        d="M12 82V38L20 30H39L44 35L49 30H68L76 38V82H64V78H60.5V68H64V42H50V68H53.5V78H50V82H38V78H34.5V68H38V42H24V68H27.5V78H24V82Z"
      />
      <rect x="86" y="14" width="12" height="12" fill={crimson} />
      <rect x="86" y="30" width="12" height="52" fill={ink} />
      <path
        fill={ink}
        d="M108 14H120V50L138 30H151V39L135 55L152 72V82H138L124 65L120 69V82H108Z"
      />
      <path fill={ink} d="M162 30H194V42H181L174 49V82H162Z" />
      <path
        fill={navy}
        fillRule="evenodd"
        d="M210 30H242L250 38V51.5H256V46L269 55L256 64V58.5H250V74L242 82H210L202 74V38ZM216 42L214 44V68L216 70H236L238 68V44L236 42Z"
      />
      <path
        fill={crimson}
        d="M260 30H308V42L278 70H297.5V60H293L301 51L309 60H304.5V70H308V82H260V70L290 42H260Z"
      />
      <rect x="318" y="14" width="12" height="12" fill={crimson} />
      <rect x="318" y="30" width="12" height="52" fill={ink} />
      <path
        fill={mustard}
        fillRule="evenodd"
        d="M342 30H380L388 38V82H348L340 74V57L348 49H376V41H342ZM352 60H376V71H352Z"
      />
    </svg>
  );
}

interface MikroziaMarkProps {
  size?: number;
  className?: string;
  style?: CSSProperties;
}

export function MikroziaMark({ size = 28, className, style }: MikroziaMarkProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 64 64"
      width={size}
      height={size}
      className={className}
      style={{ display: 'block', flexShrink: 0, ...style }}
      role="img"
      aria-label="Mikrozia"
    >
      <rect x="2" y="2" width="60" height="60" fill="#F5F0E6" stroke="#111111" strokeWidth="4" />
      <rect x="44" y="8" width="12" height="10" fill="#B81D3A" />
      <rect x="30" y="8" width="11" height="10" fill="#213B5E" />
      <rect x="8" y="8" width="19" height="10" fill="#D9A426" stroke="#111111" strokeWidth="2" />
      <path
        fill="#111111"
        d="M8 56V29L13.5 23.5H28.5L32 27L35.5 23.5H50.5L56 29V56H47V53H44.5V46H47V32H36.5V46H39V53H36.5V56H27.5V53H25V46H27.5V32H17V46H19.5V53H17V56Z"
      />
    </svg>
  );
}
