import { useEffect } from 'react';

export function useKeyboardShortcut(
  key: string,
  callback: (e: KeyboardEvent) => void,
  options: {
    ctrlOrCmd?: boolean;
    shift?: boolean;
    alt?: boolean;
    preventDefault?: boolean;
  } = {},
) {
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (options.ctrlOrCmd) {
        const hasModifier = event.ctrlKey || event.metaKey;
        if (!hasModifier) return;
      }
      if (options.shift && !event.shiftKey) return;
      if (options.alt && !event.altKey) return;

      if (event.key.toLowerCase() === key.toLowerCase()) {
        if (options.preventDefault !== false) {
          event.preventDefault();
        }
        callback(event);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [key, callback, options]);
}
