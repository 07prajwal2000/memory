import CodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { useComputedColorScheme, Box } from '@mantine/core';

export interface CodeMirrorEditorProps {
  value: string;
  onChange: (val: string) => void;
  height?: string;
  readOnly?: boolean;
  placeholder?: string;
}

export function CodeMirrorEditor({
  value,
  onChange,
  height = '500px',
  readOnly = false,
  placeholder = 'Write markdown content here...',
}: CodeMirrorEditorProps) {
  const colorScheme = useComputedColorScheme('dark');
  const isDark = colorScheme === 'dark';

  return (
    <Box
      style={{
        border: '1px solid var(--mantine-color-default-border)',
        borderRadius: '4px',
        overflow: 'hidden',
        fontSize: '13px',
      }}
    >
      <CodeMirror
        value={value}
        height={height}
        theme={isDark ? 'dark' : 'light'}
        extensions={[markdown()]}
        onChange={onChange}
        readOnly={readOnly}
        placeholder={placeholder}
        basicSetup={{
          lineNumbers: true,
          highlightActiveLineGutter: true,
          highlightSpecialChars: true,
          history: true,
          foldGutter: true,
          drawSelection: true,
          dropCursor: true,
          allowMultipleSelections: true,
          indentOnInput: true,
          syntaxHighlighting: true,
          bracketMatching: true,
          closeBrackets: true,
          autocompletion: true,
          rectangularSelection: true,
          crosshairCursor: true,
          highlightActiveLine: true,
          highlightSelectionMatches: true,
          closeBracketsKeymap: true,
          defaultKeymap: true,
          searchKeymap: true,
          historyKeymap: true,
          foldKeymap: true,
          completionKeymap: true,
          lintKeymap: true,
        }}
      />
    </Box>
  );
}
