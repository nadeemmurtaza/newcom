import { useEffect, useState } from 'react';

export default function Mailbox() {
  const [messages, setMessages] = useState([]);
  const [activeFolder, setActiveFolder] = useState('Inbox');
  const [selectedMessage, setSelectedMessage] = useState(null);
  
  const mailboxId = "11111111-1111-1111-1111-111111111111";

  useEffect(() => {
    fetch(`http://localhost:8080/api/mailbox/messages?mailbox_id=${mailboxId}&folder=${activeFolder}`)
      .then(res => res.json())
      .then(data => {
        setMessages(data || []);
        setSelectedMessage(null);
      })
      .catch(err => console.error(err));
  }, [activeFolder]);

  const toggleStar = (id) => {
    fetch(`http://localhost:8080/api/mailbox/messages/${id}/star`, { method: 'PUT' })
      .then(() => {
        setMessages(msgs => msgs.map(m => m.id === id ? { ...m, is_starred: !m.is_starred } : m));
        if (selectedMessage?.id === id) {
          setSelectedMessage({ ...selectedMessage, is_starred: !selectedMessage.is_starred });
        }
      });
  };

  const moveToFolder = (id, folderName) => {
    fetch(`http://localhost:8080/api/mailbox/messages/${id}/move?folder=${folderName}`, { method: 'PUT' })
      .then(() => {
        setMessages(msgs => msgs.filter(m => m.id !== id));
        setSelectedMessage(null);
      });
  };

  return (
    <div style={{ display: 'flex', height: '100%', background: '#fff' }}>
      
      {/* Mailbox Folders Sidebar */}
      <div style={{ width: '200px', background: '#f1f5f9', borderRight: '1px solid #e2e8f0' }}>
        <ul style={{ listStyle: 'none', padding: '1rem', margin: 0 }}>
          {['Inbox', 'Sent', 'Drafts', 'Trash', 'Archive'].map(folder => (
            <li 
              key={folder}
              onClick={() => setActiveFolder(folder)}
              style={{
                padding: '0.75rem',
                cursor: 'pointer',
                background: activeFolder === folder ? '#e2e8f0' : 'transparent',
                borderRadius: '6px',
                marginBottom: '0.25rem',
                color: '#334155',
                fontWeight: activeFolder === folder ? 'bold' : 'normal'
              }}
            >
              {folder}
            </li>
          ))}
        </ul>
      </div>

      {/* Message List */}
      <div style={{ width: '350px', borderRight: '1px solid #e2e8f0', display: 'flex', flexDirection: 'column' }}>
        <div style={{ padding: '1.25rem 1rem', borderBottom: '1px solid #e2e8f0', background: '#fff' }}>
          <h2 style={{ margin: 0, fontSize: '1.25rem' }}>{activeFolder}</h2>
        </div>
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {messages.length === 0 ? (
            <p style={{ padding: '2rem 1rem', color: '#94a3b8', textAlign: 'center' }}>No messages</p>
          ) : (
            messages.map(msg => (
              <div 
                key={msg.id} 
                onClick={() => setSelectedMessage(msg)}
                style={{
                  padding: '1rem',
                  borderBottom: '1px solid #f1f5f9',
                  cursor: 'pointer',
                  background: selectedMessage?.id === msg.id ? '#f8fafc' : '#fff'
                }}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
                  <strong style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', color: '#0f172a' }}>
                    {msg.sender}
                  </strong>
                  <span onClick={(e) => { e.stopPropagation(); toggleStar(msg.id); }} style={{ cursor: 'pointer' }}>
                    {msg.is_starred ? '⭐' : '☆'}
                  </span>
                </div>
                <div style={{ fontSize: '0.9rem', color: '#64748b', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {msg.subject}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Message Preview */}
      <div style={{ flex: 1, padding: '2rem', overflowY: 'auto' }}>
        {selectedMessage ? (
          <div>
            <div style={{ display: 'flex', gap: '0.75rem', marginBottom: '2rem' }}>
              <button 
                onClick={() => moveToFolder(selectedMessage.id, 'Archive')}
                style={{ padding: '0.5rem 1rem', background: '#fff', border: '1px solid #cbd5e1', borderRadius: '4px', cursor: 'pointer' }}
              >
                Archive
              </button>
              <button 
                onClick={() => moveToFolder(selectedMessage.id, 'Trash')}
                style={{ padding: '0.5rem 1rem', background: '#fff', border: '1px solid #fca5a5', color: '#ef4444', borderRadius: '4px', cursor: 'pointer' }}
              >
                Delete
              </button>
            </div>
            
            <h1 style={{ margin: '0 0 1rem 0', color: '#0f172a' }}>{selectedMessage.subject}</h1>
            <div style={{ marginBottom: '2rem', color: '#64748b', borderBottom: '1px solid #e2e8f0', paddingBottom: '1rem' }}>
              <strong>From:</strong> {selectedMessage.sender} <br/>
              <strong>Date:</strong> {new Date(selectedMessage.created_at).toLocaleString()}
            </div>

            <div 
              style={{ color: '#334155', lineHeight: '1.6' }}
              dangerouslySetInnerHTML={{ __html: selectedMessage.body_html || selectedMessage.body_text }} 
            />
          </div>
        ) : (
          <div style={{ color: '#94a3b8', textAlign: 'center', marginTop: '10rem' }}>
            Select a message to read
          </div>
        )}
      </div>
    </div>
  );
}
