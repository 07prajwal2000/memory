import React, { useState, useRef, useEffect, useCallback } from 'react';
import { Box } from '@mantine/core';
import { safeSetLocalStorage } from './MemoryWorkspaceContext';

export interface ResizablePaneProps {
  children: React.ReactNode;
  width: number;
  isCollapsed: boolean;
  minWidth?: number;
  maxWidth?: number;
  onWidthChange: (width: number) => void;
}

export function ResizablePane({
  children,
  width,
  isCollapsed,
  minWidth = 220,
  maxWidth = 600,
  onWidthChange,
}: ResizablePaneProps) {
  const [isDragging, setIsDragging] = useState(false);
  const startXRef = useRef(0);
  const startWidthRef = useRef(width);
  const currentWidthRef = useRef(width);

  useEffect(() => {
    currentWidthRef.current = width;
  }, [width]);

  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    setIsDragging(true);
    startXRef.current = e.clientX;
    startWidthRef.current = currentWidthRef.current;
  };

  const handleMouseMove = useCallback(
    (e: MouseEvent) => {
      const delta = e.clientX - startXRef.current;
      const nextWidth = Math.min(maxWidth, Math.max(minWidth, startWidthRef.current + delta));
      onWidthChange(nextWidth);
      safeSetLocalStorage('mem_panel_width', String(Math.round(nextWidth)));
    },
    [minWidth, maxWidth, onWidthChange],
  );

  const handleMouseUp = useCallback(() => {
    setIsDragging(false);
  }, []);

  useEffect(() => {
    if (isDragging) {
      window.addEventListener('mousemove', handleMouseMove);
      window.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    } else {
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }
    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
  }, [isDragging, handleMouseMove, handleMouseUp]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    let nextWidth: number | null = null;
    const step = e.shiftKey ? 20 : 10;

    if (e.key === 'ArrowLeft') {
      nextWidth = Math.max(minWidth, width - step);
    } else if (e.key === 'ArrowRight') {
      nextWidth = Math.min(maxWidth, width + step);
    } else if (e.key === 'Home') {
      nextWidth = minWidth;
    } else if (e.key === 'End') {
      nextWidth = maxWidth;
    }

    if (nextWidth !== null) {
      e.preventDefault();
      onWidthChange(nextWidth);
      safeSetLocalStorage('mem_panel_width', String(Math.round(nextWidth)));
    }
  };

  if (isCollapsed) {
    return null;
  }

  return (
    <Box
      style={{
        display: 'flex',
        flexDirection: 'row',
        height: '100%',
        width: `${width}px`,
        flexShrink: 0,
        position: 'relative',
      }}
    >
      <Box
        style={{
          width: '100%',
          height: '100%',
          overflow: 'hidden',
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        {children}
      </Box>

      {/* Resize drag handle */}
      <Box
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-valuenow={Math.round(width)}
        aria-valuemin={minWidth}
        aria-valuemax={maxWidth}
        aria-label="Resize memories panel"
        onMouseDown={handleMouseDown}
        onKeyDown={handleKeyDown}
        style={{
          width: '6px',
          height: '100%',
          cursor: 'col-resize',
          flexShrink: 0,
          backgroundColor: isDragging ? 'var(--mantine-color-accent-light)' : 'transparent',
          borderRight: '1px solid var(--mantine-color-default-border)',
          transition: 'background-color 150ms ease',
          outline: 'none',
        }}
      />
    </Box>
  );
}
