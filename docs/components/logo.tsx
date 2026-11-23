import { cn } from '@/lib/cn';

// Official Mikrozia logo (media/mikrozia-{light,dark}.svg).
// Theme-aware via Tailwind's `dark:` variant. Pass a height class (e.g. `h-6`);
// width scales automatically (the artwork is 2:1).
export function Logo({ className }: { className?: string }) {
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/logo-light.svg" alt="Mikrozia" className={cn('w-auto dark:hidden', className)} />
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src="/logo-dark.svg"
        alt="Mikrozia"
        className={cn('hidden w-auto dark:block', className)}
      />
    </>
  );
}
