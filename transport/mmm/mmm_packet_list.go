// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ilist provides the implementation of intrusive linked lists.
package mmm

// List is an intrusive list. Entries can be added to or removed from the list
// in O(1) time and with no additional memory allocations.
//
// The zero value for List is an empty list ready to use.
//
// To iterate over a list (where l is a List):
//      for e := l.Front(); e != nil; e = e.Next() {
// 		// do something with e.
//      }
type mmmPacketList struct {
	head *mmmPacket
	tail *mmmPacket
}

// Reset resets list l to the empty state.
func (l *mmmPacketList) Reset() {
	l.head = nil
	l.tail = nil
}

// Empty returns true iff the list is empty.
func (l *mmmPacketList) Empty() bool {
	return l.head == nil
}

// Front returns the first element of list l or nil.
func (l *mmmPacketList) Front() *mmmPacket {
	return l.head
}

// Back returns the last element of list l or nil.
func (l *mmmPacketList) Back() *mmmPacket {
	return l.tail
}

// PushFront inserts the element e at the front of list l.
func (l *mmmPacketList) PushFront(e *mmmPacket) {
	e.SetNext(l.head)
	e.SetPrev(nil)

	if l.head != nil {
		l.head.SetPrev(e)
	} else {
		l.tail = e
	}

	l.head = e
}

// PushBack inserts the element e at the back of list l.
func (l *mmmPacketList) PushBack(e *mmmPacket) {
	e.SetNext(nil)
	e.SetPrev(l.tail)

	if l.tail != nil {
		l.tail.SetNext(e)
	} else {
		l.head = e
	}

	l.tail = e
}

// PushBackList inserts list m at the end of list l, emptying m.
func (l *mmmPacketList) PushBackList(m *mmmPacketList) {
	if l.head == nil {
		l.head = m.head
		l.tail = m.tail
	} else if m.head != nil {
		l.tail.SetNext(m.head)
		m.head.SetPrev(l.tail)

		l.tail = m.tail
	}

	m.head = nil
	m.tail = nil
}

// InsertAfter inserts e after b.
func (l *mmmPacketList) InsertAfter(b, e *mmmPacket) {
	a := b.Next()
	e.SetNext(a)
	e.SetPrev(b)
	b.SetNext(e)

	if a != nil {
		a.SetPrev(e)
	} else {
		l.tail = e
	}
}

// InsertBefore inserts e before a.
func (l *mmmPacketList) InsertBefore(a, e *mmmPacket) {
	b := a.Prev()
	e.SetNext(a)
	e.SetPrev(b)
	a.SetPrev(e)

	if b != nil {
		b.SetNext(e)
	} else {
		l.head = e
	}
}

// Remove removes e from l.
func (l *mmmPacketList) Remove(e *mmmPacket) {
	prev := e.Prev()
	next := e.Next()

	if prev != nil {
		prev.SetNext(next)
	} else {
		l.head = next
	}

	if next != nil {
		next.SetPrev(prev)
	} else {
		l.tail = prev
	}
}

// Entry is a default implementation of Linker. Users can add anonymous fields
// of this type to their structs to make them automatically implement the
// methods needed by List.
type mmmPacketEntry struct {
	next *mmmPacket
	prev *mmmPacket
}

// Next returns the entry that follows e in the list.
func (e *mmmPacketEntry) Next() *mmmPacket {
	return e.next
}

// Prev returns the entry that precedes e in the list.
func (e *mmmPacketEntry) Prev() *mmmPacket {
	return e.prev
}

// SetNext assigns 'entry' as the entry that follows e in the list.
func (e *mmmPacketEntry) SetNext(entry *mmmPacket) {
	e.next = entry
}

// SetPrev assigns 'entry' as the entry that precedes e in the list.
func (e *mmmPacketEntry) SetPrev(entry *mmmPacket) {
	e.prev = entry
}
